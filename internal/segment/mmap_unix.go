//go:build unix

package segment

import (
	"fmt"
	"os"
	"syscall"
)

// Open memory-maps the segment at path and validates it.
//
// mmap asks the OS to make the file's bytes addressable as a []byte without
// reading them. Pages load from disk on first touch (a page fault) and then
// live in the OS page cache, shared by every process mapping the file and
// kept warm across shard restarts. Validation reads the whole file once (the
// checksum), so after Open the segment is warm unless memory pressure evicts it.
func Open(path string) (*Segment, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("segment: open: %w", err)
	}
	defer func() { _ = f.Close() }() // read-only; the mapping stays valid after close

	st, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("segment: stat: %w", err)
	}
	size := st.Size()
	if size < headerSize+footerSize {
		return nil, fmt.Errorf("%w: %s is %d bytes", ErrCorrupt, path, size)
	}
	if int64(int(size)) != size {
		return nil, fmt.Errorf("segment: %s is too large to map", path)
	}
	data, err := syscall.Mmap(int(f.Fd()), 0, int(size), syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("segment: mmap %s: %w", path, err)
	}
	s, err := FromBytes(data)
	if err != nil {
		_ = syscall.Munmap(data)
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	s.closer = func() error { return syscall.Munmap(data) }
	return s, nil
}
