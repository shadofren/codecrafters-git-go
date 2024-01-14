package main

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

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
	/* fmt.Println("print tree") */
	/* printBytesInHex(content) */
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

func printBytesInHex(data []byte) {
	for _, b := range data {
		fmt.Printf("%02x ", b)
	}
	fmt.Println() // Add a newline after printing the bytes
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

func Init() {
	for _, dir := range []string{".git", ".git/objects", ".git/refs"} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating directory: %s\n", err)
		}
	}
	headFileContents := []byte("ref: refs/heads/master\n")
	if err := os.WriteFile(".git/HEAD", headFileContents, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing file: %s\n", err)
	}

	fmt.Println("Initialized git directory")

}

func CatFile(objectSha string) {

	filename := filepath.Join(".git/objects", objectSha[:2], objectSha[2:])
	fileContent, err := os.ReadFile(filename)
	must(err)
	data, err := decompressZlib(bytes.NewBuffer(fileContent))
	dataBytes := data.Bytes()
	must(err)
	header, content := Cut(dataBytes, 0x00)
	objectType, _ := Cut(header, 0x20)
	_ = objectType
	blob := &GitBlob{Content: content}
	fmt.Print(string(blob.Content))
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

func ListTree(treeSha string) {
	filename := filepath.Join(".git/objects", treeSha[:2], treeSha[2:])
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
		fmt.Println(string(entry.Name))
		tree.Entry = append(tree.Entry, &entry)
	}
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
	outfile := filepath.Join(".git/objects", hash[:2], hash[2:])
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
	outfile := filepath.Join(".git/objects", hash[:2], hash[2:])
	writeFile(outfile, content)

	fmt.Println(hash)
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
