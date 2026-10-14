package segment

import (
	"bufio"
	"cmp"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"slices"
)

// Entry is one posting to write: a hash, a local track id, an anchor frame.
type Entry struct {
	Hash  uint32
	Track uint32
	T     uint32
}

// Meta describes the segment being written.
type Meta struct {
	Partition   uint32
	NPartitions uint32
	NTracks     uint32 // local tracks; every Entry.Track must be below this
	Encoding    Encoding
}

// Write writes entries as a segment at path. It sorts entries in place by
// (Hash, Track, T). The file appears atomically: it is written to a
// temporary name, synced, then renamed, so readers never see a partial file.
func Write(path string, meta Meta, entries []Entry) (err error) {
	if meta.NPartitions == 0 || meta.Partition >= meta.NPartitions {
		return fmt.Errorf("segment: partition %d of %d is invalid", meta.Partition, meta.NPartitions)
	}
	if meta.Encoding != EncodingFixed && meta.Encoding != EncodingVarint {
		return fmt.Errorf("segment: unknown encoding %d", meta.Encoding)
	}
	slices.SortFunc(entries, func(a, b Entry) int {
		return cmp.Or(cmp.Compare(a.Hash, b.Hash), cmp.Compare(a.Track, b.Track), cmp.Compare(a.T, b.T))
	})
	for _, e := range entries {
		if e.Track >= meta.NTracks {
			return fmt.Errorf("segment: track %d >= n_tracks %d", e.Track, meta.NTracks)
		}
	}

	keys, offsets, blob := encode(entries, meta.Encoding)
	hdr := header{
		Version:     Version,
		Flags:       uint16(meta.Encoding),
		NKeys:       uint32(len(keys)),
		NTracks:     meta.NTracks,
		NPostings:   uint64(len(entries)),
		Partition:   meta.Partition,
		NPartitions: meta.NPartitions,
	}

	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("segment: create: %w", err)
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()

	crc := crc32.New(crcTable())
	bw := bufio.NewWriterSize(io.MultiWriter(f, crc), 1<<20)
	if err := writeBody(bw, hdr, keys, offsets, blob); err != nil {
		return fmt.Errorf("segment: write: %w", err)
	}
	if err := bw.Flush(); err != nil {
		return fmt.Errorf("segment: write: %w", err)
	}
	if err := binary.Write(f, binary.LittleEndian, crc.Sum32()); err != nil {
		return fmt.Errorf("segment: write footer: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("segment: sync: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("segment: close: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("segment: rename: %w", err)
	}
	return nil
}

func writeBody(w io.Writer, hdr header, keys []uint32, offsets []uint64, blob []byte) error {
	if _, err := w.Write(hdr.marshal()); err != nil {
		return err
	}
	buf := make([]byte, 8)
	for _, k := range keys {
		binary.LittleEndian.PutUint32(buf, k)
		if _, err := w.Write(buf[:4]); err != nil {
			return err
		}
	}
	pad := offsetsStart(uint64(len(keys))) - (headerSize + 4*uint64(len(keys)))
	if _, err := w.Write(make([]byte, pad)); err != nil {
		return err
	}
	for _, o := range offsets {
		binary.LittleEndian.PutUint64(buf, o)
		if _, err := w.Write(buf); err != nil {
			return err
		}
	}
	_, err := w.Write(blob)
	return err
}

// encode turns sorted entries into keys, offsets, and the postings blob.
func encode(entries []Entry, enc Encoding) (keys []uint32, offsets []uint64, blob []byte) {
	offsets = []uint64{0}
	for i := 0; i < len(entries); {
		j := i
		for j < len(entries) && entries[j].Hash == entries[i].Hash {
			j++
		}
		keys = append(keys, entries[i].Hash)
		blob = appendPostings(blob, entries[i:j], enc)
		offsets = append(offsets, uint64(len(blob)))
		i = j
	}
	return keys, offsets, blob
}

func appendPostings(dst []byte, run []Entry, enc Encoding) []byte {
	if enc == EncodingFixed {
		for _, e := range run {
			dst = binary.LittleEndian.AppendUint32(dst, e.Track)
			dst = binary.LittleEndian.AppendUint32(dst, e.T)
		}
		return dst
	}
	var prevTrack, prevT uint32
	for i, e := range run {
		switch {
		case i == 0:
			dst = binary.AppendUvarint(dst, uint64(e.Track))
			dst = binary.AppendUvarint(dst, uint64(e.T))
		case e.Track != prevTrack:
			dst = binary.AppendUvarint(dst, uint64(e.Track-prevTrack))
			dst = binary.AppendUvarint(dst, uint64(e.T))
		default:
			dst = binary.AppendUvarint(dst, 0)
			dst = binary.AppendUvarint(dst, uint64(e.T-prevT))
		}
		prevTrack, prevT = e.Track, e.T
	}
	return dst
}
