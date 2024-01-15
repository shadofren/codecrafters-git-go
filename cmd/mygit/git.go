package main

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// ref: https://github.com/git/git/blob/830b4a04c45bf0a6db26defe02ed1f490acd18ee/Documentation/gitformat-pack.txt#L70-L79
	OBJECT_COMMIT    = 1
	OBJECT_TREE      = 2
	OBJECT_BLOB      = 3
	OBJECT_TAG       = 4
	OBJECT_OFS_DELTA = 6
	OBJECT_REF_DELTA = 7

	msbMask      = uint8(0b10000000)
	remMask      = uint8(0b01111111)
	objMask      = uint8(0b01110000)
	firstRemMask = uint8(0b00001111)
)

var shaToObj map[string]Object = make(map[string]Object)

// Plain object for cloning purpose
type Object struct {
	Type byte // object type.
	Buf  []byte
}

type GitObject interface {
	Serialize() (string, []byte)
}

type GitBlob struct {
	Content []byte
}

type TreeEntry struct {
	Perm []byte
	Name []byte
	Hash [20]byte
}

var author = "Manh Tu <xxlaguna93@gmail.com>"
var filePerm = []byte{'1', '0', '0', '6', '4', '4'}
var dirPerm = []byte{'4', '0', '0', '0', '0'}

func NewTreeEntry(filename string) *TreeEntry {
	objectHash := HashObject(filename)
	hashBytes, err := hex.DecodeString(objectHash)
	must(err)
	var hash [20]byte
	copy(hash[:], hashBytes)
	baseName := filepath.Base(filename)
	return &TreeEntry{Perm: filePerm, Name: []byte(baseName), Hash: hash}
}

type GitTree struct {
	Entry []*TreeEntry
}

func (e *TreeEntry) Serialize() []byte {
	content := e.Perm[:]
	content = append(content, 0x20)
	content = append(content, e.Name...)
	content = append(content, 0x00)
	content = append(content, e.Hash[:]...)
	return content
}

func (t *GitTree) Serialize() (string, []byte) {
	content := []byte("tree ")
	entries := []byte{}
	for _, entry := range t.Entry {
		entries = append(entries, entry.Serialize()...)
	}
	content = append(content, []byte(strconv.Itoa((len(entries))))...)
	content = append(content, 0x00)
	content = append(content, entries...)
	hash, err := calcSHA1(content)
	must(err)
	compressed, err := compressZlib(bytes.NewBuffer(content))
	must(err)
	compressedBytes := compressed.Bytes()
	return hash, compressedBytes
}

type GitCommit struct {
	Tree    string
	Parent  string
	Author  string
	Email   string
	Time    time.Time
	Message string
}

func (c *GitCommit) Serialize() (string, []byte) {
	timeFormat := c.Time.Unix()
	location, _ := c.Time.Zone()
	fileContent := fmt.Sprintf("tree %s\nparent %s\nauthor %s %s %d %s00\ncommitter %s %s %d %s00\n\n%s\n",
		c.Tree, c.Parent,
		c.Author, c.Email, timeFormat, location,
		c.Author, c.Email, timeFormat, location,
		c.Message)
	content := []byte("commit ")
	content = append(content, []byte(strconv.Itoa((len(fileContent))))...)
	content = append(content, 0x00)
	content = append(content, []byte(fileContent)...)
	hash, err := calcSHA1(content)
	must(err)
	compressed, err := compressZlib(bytes.NewBuffer(content))
	must(err)
	compressedBytes := compressed.Bytes()
	return hash, compressedBytes
}

func (o *GitBlob) Serialize() (string, []byte) {
	content := []byte("blob ")
	content = append(content, []byte(strconv.Itoa((len(o.Content))))...)
	content = append(content, 0x00)
	content = append(content, o.Content...)
	hash, err := calcSHA1(content)
	must(err)
	compressed, err := compressZlib(bytes.NewBuffer(content))
	must(err)
	compressedBytes := compressed.Bytes()
	return hash, compressedBytes
}

func Init(root string) {
	for _, dir := range []string{".git", ".git/objects", ".git/refs"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating directory: %s\n", err)
		}
	}
	headFileContents := []byte("ref: refs/heads/master\n")
	if err := os.WriteFile(filepath.Join(root, ".git/HEAD"), headFileContents, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing file: %s\n", err)
	}

	fmt.Println("Initialized git directory")
}

func CatFile(localDir, objectSha string) ([]byte, error) {

	filename := filepath.Join(localDir, objectPath(objectSha))
	fileContent, err := os.ReadFile(filename)
	must(err)
	data, err := decompressZlib(bytes.NewBuffer(fileContent))
	dataBytes := data.Bytes()
	must(err)
	header, content := Cut(dataBytes, 0x00)
	objectType, objectLen := Cut(header, 0x20)
	_ = objectType
	_ = objectLen
	return content, nil
}

func HashObject(filename string) string {
	file, err := os.Open(filename)
	must(err)

	content, err := io.ReadAll(file)
	must(err)
	blob := &GitBlob{Content: content}
	hash, data := blob.Serialize()

	object := filepath.Join(".git/objects", hash[:2], hash[2:])
	writeFile(object, data)
	return hash
}

func ListTree(localDir, treeSha string) *GitTree {
	filename := filepath.Join(localDir, objectPath(treeSha))
	fileContent, err := os.ReadFile(filename)
	must(err)
	data, err := decompressZlib(bytes.NewBuffer(fileContent))
	dataBytes := data.Bytes()
	must(err)
	header, content := Cut(dataBytes, 0x00)
	treeType, _ := Cut(header, 0x20)
	_ = treeType
	tree := &GitTree{Entry: make([]*TreeEntry, 0)}
	reader := bytes.NewReader(content)
	for {
		var entry TreeEntry
		entry.Perm, err = readUntil(reader, 0x20)
		if err != nil {
			if err == io.EOF {
				break
			}
			must(err)
		}
		entry.Name, err = readUntil(reader, 0x00)
		must(err)
		reader.Read(entry.Hash[:])
		tree.Entry = append(tree.Entry, &entry)
	}
	return tree
}

func WriteTree(root string) string {
	tree := &GitTree{make([]*TreeEntry, 0)}
	_ = tree

	entries, err := os.ReadDir(root)
	if err != nil {
		fmt.Println(err)
	}
	for _, entry := range entries {
		if entry.Name() == ".git" {
			continue
		}
		fullPath := filepath.Join(root, entry.Name())
		if entry.IsDir() {
			// recursively create the blob, skip for now
			dirHash := WriteTree(fullPath)
			hashBytes, err := hex.DecodeString(dirHash)
			must(err)
			var hash [20]byte
			copy(hash[:], hashBytes)
			dirEntry := &TreeEntry{Perm: dirPerm, Name: []byte(entry.Name()), Hash: hash}
			tree.Entry = append(tree.Entry, dirEntry)
		} else {
			info, _ := entry.Info()
			mode := fmt.Sprintf("100%03o", info.Mode().Perm()) // Get Unix permissions as octal string
			treeEntry := NewTreeEntry(fullPath)
			treeEntry.Perm = []byte(mode)
			tree.Entry = append(tree.Entry, treeEntry)
		}
	}
	hash, content := tree.Serialize()
	outfile := objectPath(hash)
	writeFile(outfile, content)
	return hash
}

func CommitTree(treeSha, parentSha, message string) {
	commit := &GitCommit{
		Tree:    treeSha,
		Parent:  parentSha,
		Author:  "Manh Tu",
		Email:   "xxlaguna93@gmail.com",
		Time:    time.Now(),
		Message: message,
	}

	hash, content := commit.Serialize()
	outfile := objectPath(hash)
	writeFile(outfile, content)

	fmt.Println(hash)
}

func Clone(repo, localDir string) {
	Init(localDir)

	commitSha, err := fetchLatestCommitHash(repo)
	must(err)
	fmt.Println("commit sha", commitSha)

	err = writeBranchRefFile(localDir, "master", commitSha)
	must(err)

	err = fetchObjects(repo, commitSha)
	must(err)

	err = writeFetchedObjects(localDir)
	must(err)

	err = restoreRepository(localDir, commitSha)
	must(err)
}

func fetchObjects(repoUrl, commitSha string) error {
	packfileBuf := fetchPackfile(repoUrl, commitSha)

	// parse packfile for debugging
	sign := packfileBuf[:4]
	version := binary.BigEndian.Uint32(packfileBuf[4:8])
	numObjects := binary.BigEndian.Uint32(packfileBuf[8:12])
	log.Printf("[Debug] packfile sign: %s\n", string(sign))
	log.Printf("[Debug] version: %d\n", version)
	log.Printf("[Debug] num objects: %d\n", numObjects)

	// verify checksum
	checkumLen := 20
	storedChecksum := packfileBuf[len(packfileBuf)-checkumLen:]
	actualChecksum := sha1.Sum(packfileBuf[:len(packfileBuf)-checkumLen])
	if !bytes.Equal(storedChecksum, actualChecksum[:]) {
		return fmt.Errorf("expected checksum: %v, got %v", storedChecksum, actualChecksum)
	}

	headerLen := 12
	bufReader := bytes.NewReader(packfileBuf[headerLen:])
	for i := 0; i < int(numObjects); i++ {
		err := readObject(bufReader)
		if err != nil {
			return err
		}
	}
	return nil
}

func readSha(reader *bytes.Reader) (string, error) {
	sha := make([]byte, 20)
	if _, err := reader.Read(sha); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha), nil
}

func objectPath(sha string) string {
	return filepath.Join(".git", "objects", sha[:2], sha[2:])
}

// Read objects from packfile.
func readObject(reader *bytes.Reader) error {
	objType, objLen, err := readObjectTypeAndLen(reader)
	if err != nil {
		return err
	}

	switch objType {
	case OBJECT_REF_DELTA:
		baseObjSha, err := readSha(reader)
		if err != nil {
			return err
		}
		baseObj, ok := shaToObj[baseObjSha]
		if !ok {
			return fmt.Errorf("unknown obj sha: %s", baseObjSha)
		}
		decompressed, err := decompressObject(reader)
		if err != nil {
			return err
		}

		deltified, err := readDeltified(decompressed, &baseObj)
		if err != nil {
			return err
		}

		obj := Object{
			Type: baseObj.Type,
			Buf:  deltified.Bytes(),
		}
		if err := saveObj(&obj); err != nil {
			return err
		}
	case OBJECT_OFS_DELTA:
		// TODO : Implement this section
		return errors.New("Unsupported")
	default:
		decompressed, err := decompressObject(reader)
		if err != nil {
			return err
		}
		obj := Object{
			Type: objType,
			Buf:  decompressed.Bytes(),
		}
		if objLen != decompressed.Len() {
		    fmt.Println("object doesn't match", objType, decompressed)
		    fmt.Println("expected length", objLen, "actual", decompressed.Len())
		}
		if err := saveObj(&obj); err != nil {
			return err
		}
	}
	return nil
}

func decompressObject(reader *bytes.Reader) (*bytes.Buffer, error) {
	decompressedReader, err := zlib.NewReader(reader)
	if err != nil {
		return nil, err
	}
	decompressed := bytes.NewBuffer([]byte{})
	if _, err := io.Copy(decompressed, decompressedReader); err != nil {
		return nil, err
	}
	return decompressed, nil
}

// ref: https://git-scm.com/docs/pack-format#_deltified_representation
func readDeltified(reader *bytes.Buffer, baseObj *Object) (*bytes.Buffer, error) {
	// srcObjLen, err := binary.ReadUvarint(reader)
	_, err := binary.ReadUvarint(reader)
	if err != nil {
		return nil, err
	}
	// log.Printf("[Debug] base len: %d\n", srcObjLen)
	dstObjLen, err := binary.ReadUvarint(reader)
	if err != nil {
		return nil, err
	}
	// log.Printf("[Debug] deltified len: %d\n", dstObjLen)
	result := bytes.NewBuffer([]byte{})
	for reader.Len() > 0 {
		firstByte, err := reader.ReadByte()
		if err != nil {
			return nil, err
		}
		// log.Printf("[Debug] first byte: %b\n", firstByte)
		if (firstByte & msbMask) == 0 {
			// Add new data.
			n := int64(firstByte & remMask)
			if _, err := io.CopyN(result, reader, n); err != nil {
				return nil, err
			}
		} else { // msb == 1
			// Copy data.
			offset := 0
			size := 0
			// Check offset byte.
			for i := 0; i < 4; i++ {
				if (firstByte>>i)&1 > 0 { // i-bit is present.
					b, err := reader.ReadByte()
					if err != nil {
						return nil, err
					}
					offset += int(b) << (i * 8)
				}
			}
			// Check size byte.
			for i := 4; i < 7; i++ {
				if (firstByte>>i)&1 > 0 { // i-bit is present.
					b, err := reader.ReadByte()
					if err != nil {
						return nil, err
					}
					size += int(b) << ((i - 4) * 8)
				}
			}
			// log.Printf("[Debug] offset: %d\n", offset)
			// log.Printf("[Debug] size: %d\n", size)
			// log.Printf("[Debug] size: %b\n", size)
			if _, err := result.Write(baseObj.Buf[offset : offset+size]); err != nil {
				return nil, err
			}
		}
	}
	if result.Len() != int(dstObjLen) {
		return nil, fmt.Errorf("invalid deltified buf: expected: %d, but got: %d", dstObjLen, result.Len())
	}
	return result, nil
}
func saveObj(o *Object) error {
	objSha, err := o.sha()
	if err != nil {
		return err
	}
	shaToObj[objSha] = *o
	// log.Printf("[Debug] obj sha: %s\n", objSha)
	// log.Printf("[Debug] actual obj len: %d\n", len(o.Buf))
	return nil
}

func (o *Object) sha() (string, error) {
	b, err := o.wrappedBuf()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha1.Sum(b)), nil
}

// this might be wrong
func readObjectTypeAndLen(reader *bytes.Reader) (byte, int, error) {
	num := 0
	b, err := reader.ReadByte()
	if err != nil {
		return 0, 0, err
	}
	objType := (b & objMask) >> 4
	num += int(b & firstRemMask)
	if (b & msbMask) == 0 {
		return objType, num, nil
	}
	i := 0
	for {
		b, err := reader.ReadByte()
		if err != nil {
			return 0, 0, err
		}
		num += int(b) << (4 + 7*i)
		if (b & msbMask) == 0 {
			break
		}
		i++
	}
	// log.Printf("[Debug] varint num: %d\n", num)
	// log.Printf("[Debug] read data: %b\n", data[:i+1])
	return objType, num, nil

}
func fetchPackfile(repoUrl, commitSha string) []byte {
	buf := bytes.NewBuffer([]byte{})
	buf.WriteString(packetLine(fmt.Sprintf("want %s no-progress\n", commitSha)))
	buf.WriteString("0000") // flush
	buf.WriteString(packetLine("done\n"))
	uploadPackUrl := fmt.Sprintf("%s/git-upload-pack", repoUrl)
	resp, err := http.Post(uploadPackUrl, "", buf)
	must(err)
	defer resp.Body.Close()
	result := bytes.NewBuffer([]byte{})
	_, err = io.Copy(result, resp.Body)
	must(err)
	packfileBuf := result.Bytes()[8:] // skip like "0031ACK\n"
	return packfileBuf
}

func packetLine(rawLine string) string {
	size := len(rawLine) + 4
	return fmt.Sprintf("%04x%s", size, rawLine)
}

func writeBranchRefFile(localRepo string, branch string, commitSha string) error {
	refFilePath := filepath.Join(localRepo, ".git", "refs", "heads", branch)
	if err := os.MkdirAll(filepath.Dir(refFilePath), 0755); err != nil {
		return err
	}
	refFileContent := []byte(commitSha)
	if err := os.WriteFile(refFilePath, refFileContent, 0644); err != nil {
		return err
	}
	return nil
}

func fetchLatestCommitHash(repoUrl string) (string, error) {
	resp, err := http.Get(fmt.Sprintf("%s/info/refs?service=git-upload-pack", repoUrl))
	must(err)
	defer resp.Body.Close()
	buf := bytes.NewBuffer([]byte{})
	_, err = io.Copy(buf, resp.Body)
	must(err)
	reader := bytes.NewReader(buf.Bytes())
	// read the 001e# service=git-upload-pack
	_, err = readPacketLine(reader)
	must(err)
	// read the 0000
	_, err = readPacketLine(reader)
	must(err)
	// read the first line (HEAD)
	head, err := readPacketLine(reader)
	must(err)
	commitSha := strings.Split(string(head), " ")[0]
	return commitSha, nil
}

func readPacketLine(reader *bytes.Reader) ([]byte, error) {
	// read the first 4 byte => lengthInHex
	lengthInHex := make([]byte, 4)
	_, err := reader.Read(lengthInHex)
	if err != nil {
		return []byte{}, err
	}
	length, err := strconv.ParseInt(string(lengthInHex), 16, 64)
	if err != nil {
		return []byte{}, err
	}
	if length == 0 {
		return []byte{}, nil // 0000
	}
	data := make([]byte, length-4)
	_, err = reader.Read(data)
	return data, err
}

func writeFile(filename string, data []byte) {
	err := os.MkdirAll(filepath.Dir(filename), 0755)
	must(err)
	_ = os.WriteFile(filename, data, 0644)
}

func decompressZlib(input *bytes.Buffer) (*bytes.Buffer, error) {
	zlibReader, err := zlib.NewReader(input)
	if err != nil {
		return nil, err
	}
	defer zlibReader.Close()

	var output bytes.Buffer
	_, err = io.Copy(&output, zlibReader)
	if err != nil {
		return nil, err
	}

	return &output, nil
}

func compressZlib(input *bytes.Buffer) (*bytes.Buffer, error) {
	var output bytes.Buffer
	zlibWriter := zlib.NewWriter(&output)

	_, err := io.Copy(zlibWriter, input)
	if err != nil {
		return nil, err
	}
	zlibWriter.Close()
	return &output, nil
}

func Cut(data []byte, delim byte) ([]byte, []byte) {
	for i, b := range data {
		if b == delim {
			return data[:i], data[i+1:]
		}
	}
	return data, nil
}

func calcSHA1(data []byte) (string, error) {
	hasher := sha1.New()
	_, err := hasher.Write(data)
	if err != nil {
		return "", err
	}

	hashInBytes := hasher.Sum(nil)
	hashString := hex.EncodeToString(hashInBytes)

	return hashString, nil
}

func readUntil(reader *bytes.Reader, delim byte) ([]byte, error) {
	var result []byte
	for {
		// Read a single byte from the reader
		b, err := reader.ReadByte()
		if err != nil {
			return nil, err
		}
		// Break the loop if the byte is 0x00
		if b == delim {
			break
		}
		// Append the byte to the result slice
		result = append(result, b)
	}
	return result, nil
}

func (o *Object) wrappedBuf() ([]byte, error) {
	t, err := o.typeString()
	if err != nil {
		return []byte{}, err
	}
	wrappedBuf, err := wrapContent(o.Buf, t)
	if err != nil {
		return []byte{}, err
	}
	return wrappedBuf.Bytes(), nil
}

func (o *Object) typeString() (string, error) {
	switch o.Type {
	case OBJECT_COMMIT:
		return "commit", nil
	case OBJECT_TREE:
		return "tree", nil
	case OBJECT_BLOB:
		return "blob", nil
	case OBJECT_TAG:
		return "tag", nil
	default:
		return "", fmt.Errorf("invalid type: %d", o.Type)
	}
}

// Wrap content and returns a git object.
func wrapContent(contents []byte, objectType string) (*bytes.Buffer, error) {
	outerContents := bytes.NewBuffer([]byte{})
	outerContents.WriteString(fmt.Sprintf("%s %d\x00", objectType, len(contents)))
	if _, err := io.Copy(outerContents, bytes.NewReader(contents)); err != nil {
		return nil, err
	}
	return outerContents, nil
}

// Write objects in shaToObj to .git/objects.
func writeFetchedObjects(localRepo string) error {
	for _, object := range shaToObj {
		b, err := object.wrappedBuf()
		if err != nil {
			return err
		}
		if _, err := writeGitObject(localRepo, b); err != nil {
			return err
		}
	}
	return nil
}

// Write the git object and return the sha1.
func writeGitObject(localDir string, content []byte) (string, error) {
	blobSha := fmt.Sprintf("%x", sha1.Sum(content))
	// log.Printf("[Debug] object sha: %s\n", blobSha)

	objectFilePath := filepath.Join(localDir, objectPath(blobSha))
	if err := os.MkdirAll(filepath.Dir(objectFilePath), 0755); err != nil {
		return "", err
	}
	objectFile, err := os.Create(objectFilePath)
	if err != nil {
		return "", err
	}
	compresssedFileWriter := zlib.NewWriter(objectFile)
	if _, err = compresssedFileWriter.Write(content); err != nil {
		return "", err
	}
	if err := compresssedFileWriter.Close(); err != nil {
		return "", err
	}
	return blobSha, nil
}

func restoreRepository(repoPath, commitSha string) error {
	// Parse commit and get tree sha.
	commitBuf, err := CatFile(repoPath, commitSha)
	if err != nil {
		return err
	}
	log.Printf("[Debug] latest commit sha: %s\n", commitSha)
	commitReader := bufio.NewReader(bytes.NewReader(commitBuf))
	treePrefix, err := commitReader.ReadString(' ')
	if err != nil {
		return err
	}
	if treePrefix != "tree " {
		return fmt.Errorf("invalid commit blob: %s", string(commitBuf))
	}
	treeSha, err := commitReader.ReadString('\n')
	if err != nil {
		return err
	}
	treeSha = treeSha[:len(treeSha)-1] // Strip newline.
	// Traverse tree objects.
	if err := restoreTree(repoPath, "", treeSha); err != nil {
		return err
	}
	return nil
}

func restoreTree(repoPath, curDir, treeSha string) error {
	tree := ListTree(repoPath, treeSha)
	for _, child := range tree.Entry {
		sha := hex.EncodeToString(child.Hash[:])
		if isBlob(child.Perm) {
			// Create a file
			blobBuf, err := CatFile(repoPath, sha)
			if err != nil {
				return err
			}
			filename := filepath.Join(repoPath, curDir, string(child.Name))
			writeFile(filename, blobBuf)
		} else {
			// traverse recursively.
			childDir := filepath.Join(curDir, string(child.Name))
			if err := restoreTree(repoPath, childDir, sha); err != nil {
				return err
			}
		}
	}
	return nil
}

func isBlob(perm []byte) bool {
	return strings.HasPrefix(string(perm), "100")
}
