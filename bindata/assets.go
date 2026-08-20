package bindata

import (
	"embed"
	"sort"
)

//go:embed assets/*
var f embed.FS

// Asset reads and returns the content of the named file.
func Asset(name string) ([]byte, error) {
	return f.ReadFile(name)
}

// MustAsset reads and returns the content of the named file or panics
// if something went wrong.
func MustAsset(name string) []byte {
	data, err := f.ReadFile(name)
	if err != nil {
		panic(err)
	}

	return data
}

// AssetDir returns the sorted list of non-directory entries in the named directory.
func AssetDir(name string) ([]string, error) {
	entries, err := f.ReadDir(name)
	if err != nil {
		return nil, err
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		files = append(files, entry.Name())
	}
	sort.Strings(files)
	return files, nil
}
