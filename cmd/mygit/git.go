package main

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

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
  file, err := os.Open(filename)
  must(err)

	reader := bufio.NewReader(file)
  data, err := decompressZlib(reader)
  header, data := Cut(data, 0x00)
  _ = header
  must(err)
  fmt.Print(string(data))
}

func decompressZlib(reader *bufio.Reader) ([]byte, error) {
	// Create a zlib reader from the compressed data
	zlibReader, err := zlib.NewReader(reader)
	if err != nil {
		return nil, err
	}
	defer zlibReader.Close()

	var decompressedData bytes.Buffer
	_, err = io.Copy(&decompressedData, zlibReader)
	if err != nil {
		return nil, err
	}

	return decompressedData.Bytes(), nil
}

func Cut(data []byte, delim byte) ([]byte, []byte){
	for i, b := range data {
		if b == delim {
      return data[:i], data[i+1:]
		}
	}
	return data, nil
}
