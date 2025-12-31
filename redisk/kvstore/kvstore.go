package kvstore

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path"
	"syscall"

	"cc.io/redisk/tree"
	"golang.org/x/sys/unix"
)

const DB_SIG = "BYODKV16BYTESSSS"

type KV struct {
	Path string // file name

	// internals
	fd   int
	tree tree.BTree
	mmap struct {
		total  int      // mmap size
		chunks [][]byte // multiple mmaps
	}
	page struct {
		flushed uint64   // database size in no. of pages
		temp    [][]byte // newly-allocated pages
	}
	failed bool
}

func (db *KV) Inspect() {
	fmt.Printf("fd: %d\n", db.fd)
	fmt.Printf("tree.Root: %d\n", db.tree.Root)
	fmt.Printf("mmap.total: %d\n", db.mmap.total)
	fmt.Printf("mmap.chunks: %v\n", len(db.mmap.chunks))
	fmt.Printf("page.flushed: %d\n", db.page.flushed)
	fmt.Printf("len(page.temp): %d\n", len(db.page.temp))
}

// Read a page
func (db *KV) pageRead(pointer uint64) []byte {
	start := uint64(0)
	for _, chunk := range db.mmap.chunks {
		end := start + uint64(len(chunk))/tree.BTREE_PAGE_SIZE
		if pointer < end {
			offset := tree.BTREE_PAGE_SIZE * (pointer - start)
			return chunk[offset : offset+tree.BTREE_PAGE_SIZE]
		}
		start = end
	}
	panic("bad pointer")
}

// Collect new pages from B+tree updates and allocate the page number from the end of the DB. Not written to disk yet.
func (db *KV) pageAppend(node []byte) uint64 {
	pointer := db.page.flushed + uint64(len(db.page.temp))
	db.page.temp = append(db.page.temp, node)
	return pointer
}

// Delete page
func (db *KV) pageDel(uint64) {
	//TODO:
}

func (db *KV) Open() error {
	// Set file descriptor
	fd, err := createFileSync(db.Path)
	if err != nil {
		return err
	}
	db.fd = fd

	// Init mmap
	if err := initMmap(db); err != nil {
		return err
	}

	// Set functions
	db.tree.Get = db.pageRead
	db.tree.New = db.pageAppend
	db.tree.Del = db.pageDel

	// Read meta
	if err := readRoot(db); err != nil {
		return err
	}

	return nil
}

func (db *KV) Get(key []byte) ([]byte, bool) {
	return db.tree.Find(key)
}

func (db *KV) Set(key, val []byte) error {
	// Save in-memory state - root pointer and total DB size
	meta := saveMeta(db)

	if err := db.tree.Insert(key, val); err != nil {
		return err
	}

	return updateOrRevert(db, meta)
}

func (db *KV) Del(key []byte) (bool, error) {
	deleted, err := db.tree.Delete(key)
	if err != nil {
		return false, err
	}

	return deleted, nil
}

// Initialise mmap
func initMmap(db *KV) error {
	file := os.NewFile(uintptr(db.fd), "newFile")
	if file == nil {
		return fmt.Errorf("invalid file descriptor")
	}

	fi, err := file.Stat()
	if err != nil {
		return err
	}

	size := fi.Size()
	return extendMmap(db, int(size))
}

// Increase mmap exponentially
func extendMmap(db *KV, size int) error {
	if size <= db.mmap.total {
		return nil
	}

	// Double the current address
	alloc := max(db.mmap.total, 64<<20)

	// Adjust the allocation until it's enough
	for db.mmap.total+alloc < size {
		alloc *= 2
	}

	chunk, err := syscall.Mmap(db.fd, int64(db.mmap.total), alloc, syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return fmt.Errorf("mmap: %w", err)
	}

	db.mmap.total += alloc
	db.mmap.chunks = append(db.mmap.chunks, chunk)
	return nil
}

func writePages(db *KV) error {
	// Extend the mmap if required
	size := (int(db.page.flushed) + len(db.page.temp)) * tree.BTREE_PAGE_SIZE
	if err := extendMmap(db, size); err != nil {
		return err
	}

	// Write data pages to the file
	offset := int64(db.page.flushed * tree.BTREE_PAGE_SIZE)
	if _, err := unix.Pwritev(db.fd, db.page.temp, offset); err != nil {
		return err
	}

	// Update total DB size
	db.page.flushed += uint64(len(db.page.temp))

	// Discard in-memory data
	db.page.temp = db.page.temp[:0]
	return nil
}

func updateFile(db *KV) error {
	// Write new nodes
	if err := writePages(db); err != nil {
		return err
	}

	// fsync to enforce the order between 1 and 3
	if err := syscall.Fsync(db.fd); err != nil {
		return err
	}

	// Update the root pointer atomically
	if err := updateMeta(db); err != nil {
		return err
	}

	// fsync to persist everything
	return syscall.Fsync(db.fd)
}

func createFileSync(file string) (int, error) {
	// Obtain directory fd
	flags := os.O_RDONLY | syscall.O_DIRECTORY

	dirfd, err := syscall.Open(path.Dir(file), flags, 0o644)
	if err != nil {
		return -1, fmt.Errorf("open directory: %w", err)
	}
	defer syscall.Close(dirfd)

	// Open or create file
	flags = os.O_RDWR | os.O_CREATE
	fd, err := unix.Openat(dirfd, path.Base(file), flags, 0o644)
	if err != nil {
		return -1, fmt.Errorf("open file: %w", err)
	}

	// fsync directory
	if err = syscall.Fsync(dirfd); err != nil {
		_ = syscall.Close(fd)
		return -1, fmt.Errorf("fsync directory: %w", err)
	}

	return fd, nil
}

// Load metadata: root and total DB size
func loadMeta(db *KV, data []byte) {
	db.tree.Root = binary.LittleEndian.Uint64(data[16:])
	db.page.flushed = binary.LittleEndian.Uint64(data[24:])
}

func readRoot(db *KV) error {
	if db.mmap.total == 0 {
		db.page.flushed = 1
		return nil
	}

	// Read the page
	data := db.mmap.chunks[0]
	if !bytes.Equal([]byte(DB_SIG), data[:16]) {
		return errors.New("Bad signature")
	}

	// Verify
	loadMeta(db, data)
	return nil
}

// Get meta data to be saved
func saveMeta(db *KV) []byte {
	var data [32]byte
	copy(data[:16], []byte(DB_SIG))
	binary.LittleEndian.PutUint64(data[16:], db.tree.Root)
	binary.LittleEndian.PutUint64(data[24:], db.page.flushed)

	return data[:]
}

// Updates the meta page
func updateMeta(db *KV) error {
	if _, err := syscall.Pwrite(db.fd, saveMeta(db), 0); err != nil {
		return fmt.Errorf("write meta page: %w", err)
	}
	return nil
}

func updateOrRevert(db *KV, meta []byte) error {
	// Ensure on-disk meta page matches in-memory one after an error
	if db.failed {
		if err := updateMeta(db); err != nil {
			return err
		}
		db.failed = false
	}

	// Persist to DB
	err := updateFile(db)

	if err != nil {
		// On-disk meta page is in unknown. Mark in-memory meta page to be rewritten
		db.failed = true

		// If persisting fails, revert
		loadMeta(db, meta)

		// Discard any temp data
		db.page.temp = db.page.temp[:0]
	}
	return err
}
