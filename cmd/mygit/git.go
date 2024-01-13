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
)

type GitBlob struct {
	Content []byte
}

type TreeEntry struct {
  Perm []byte
  Name []byte
  Hash [20]byte
}
type GitTree struct {
	Entry []*TreeEntry
}

func (e *TreeEntry) Serialize() []byte {
  content := e.Perm[:]
  content = append(content, 0x20)
  content = append(content, e.Name...)
	content = append(content, 0x00)
	content = append(content, []byte(hex.EncodeToString(e.Hash[:]))...)
  return content
}

func (t *GitTree) Serialize() string {
  return "" 
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
	err = os.MkdirAll(filepath.Dir(object), 0755)
	must(err)
	err = os.WriteFile(object, data, 0644)
	must(err)
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
    // parse till space => perm
    // parse till 0x00 => filename
    // parse 20 byte => hash
    fmt.Println(string(entry.Name))
    tree.Entry = append(tree.Entry, &entry)
  }
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
