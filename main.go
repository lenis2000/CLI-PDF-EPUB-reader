package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	// Determine target: argument or current directory
	arg := "."
	if len(os.Args) > 1 {
		arg = os.Args[1]
	}

	// Expand ~ to home directory
	if strings.HasPrefix(arg, "~/") {
		homeDir, _ := os.UserHomeDir()
		arg = filepath.Join(homeDir, arg[2:])
	}

	// Check if argument is a directory or file
	info, statErr := os.Stat(arg)
	if statErr != nil {
		fmt.Printf("Path not found: %s\n", arg)
		return
	}

	// Determine the search directory for "back" functionality
	searchDir := arg
	if !info.IsDir() {
		searchDir = filepath.Dir(arg)
	}

	// Main loop - allows going back to file picker
	for {
		var filePath string
		var err error

		if info.IsDir() {
			// It's a directory - search within it
			filePath, err = selectFileWithPickerInDir(searchDir)
			if err != nil {
				fmt.Printf("File selection cancelled: %v\n", err)
				return
			}
		} else {
			// First time with a file - use directly, then switch to directory mode
			filePath = arg
			info = nil // Next iteration will use directory picker
		}

		if filePath == "" {
			return
		}

		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			fmt.Printf("File not found at: %s\n", filePath)
			return
		}

		ext := strings.ToLower(filepath.Ext(filePath))
		if ext != ".pdf" && ext != ".epub" && ext != ".docx" {
			fmt.Printf("Unsupported file format: %s\nSupported formats: .pdf, .epub, .docx\n", ext)
			return
		}

		viewer := NewDocumentViewer(filePath)
		if err := viewer.Open(); err != nil {
			fmt.Printf("Error opening file: %v\n", err)
			return
		}

		wantBack := viewer.Run()
		if !wantBack {
			return
		}
		// Loop continues - go back to file picker
	}
}

func selectFileWithPickerInDir(dir string) (string, error) {
	searcher := NewFileSearcher()
	if err := searcher.ScanDirectory(dir); err != nil {
		return "", fmt.Errorf("error scanning directory: %v", err)
	}
	allFiles := searcher.GetAllFiles()
	if len(allFiles) == 0 {
		return "", fmt.Errorf("no PDF or EPUB files found in %s", dir)
	}
	picker := NewFilePicker(searcher)
	return picker.Run()
}
