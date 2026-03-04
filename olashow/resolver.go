package olashow

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type FileResolver interface {
	Open(file string) (io.ReadCloser, error)
	Metadata() (io.ReadCloser, error)
	FileResolver(file string) FileResolver
}

type defaultResolver struct {
	rootFile string
	baseDir  string
	seen     map[string]bool
}

func NewDefaultResolver(rootFile string) *defaultResolver {
	return &defaultResolver{
		rootFile: rootFile,
		baseDir:  filepath.Dir(rootFile),
		seen: map[string]bool{
			rootFile: true,
		},
	}
}

func (r *defaultResolver) Open(name string) (io.ReadCloser, error) {
	path := r.resolve(name)
	if r.seen[path] {
		return nil, fmt.Errorf("cyclic include %q", name)
	}

	r.seen[path] = true
	return os.Open(path)
}

func (r *defaultResolver) Metadata() (io.ReadCloser, error) {
	return os.Open(r.rootFile + ".metadata")
}

func (r *defaultResolver) FileResolver(name string) FileResolver {
	path := r.resolve(name)
	nr := NewDefaultResolver(path)
	for k, v := range r.seen {
		nr.seen[k] = v
	}

	return nr
}

func (r *defaultResolver) resolve(name string) string {
	if filepath.IsAbs(name) {
		return filepath.Clean(name)
	}
	return filepath.Clean(filepath.Join(r.baseDir, name))
}
