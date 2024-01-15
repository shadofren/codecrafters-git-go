package main

import (
	"fmt"
	"os"
)

// Usage: your_git.sh <command> <arg1> <arg2> ...
func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: mygit <command> [<args>...]\n")
		os.Exit(1)
	}

	switch command := os.Args[1]; command {
	case "init":
		Init(".")
	case "cat-file":
		content, _ := CatFile(".", os.Args[3])
		fmt.Print(string(content))
	case "hash-object":
		hash := HashObject(os.Args[3])
		fmt.Print(hash)
	case "ls-tree":
		tree := ListTree(".", os.Args[3], false) // no recursion
		for _, entry := range tree.Entry {
			fmt.Println(string(entry.Name))
		}
	case "write-tree":
		hash := WriteTree(".")
		fmt.Print(hash)
	case "commit-tree":
		treeSha, parentSha, message := os.Args[2], os.Args[4], os.Args[6]
		CommitTree(treeSha, parentSha, message)
	case "clone":
		repo, localDir := os.Args[2], os.Args[3]
		Clone(repo, localDir)

	default:
		fmt.Fprintf(os.Stderr, "Unknown command %s\n", command)
		os.Exit(1)
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
