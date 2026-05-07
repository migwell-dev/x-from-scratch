package code

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"syscall"
)

const DB_SIG = "migwelldevDB01"

type KV struct {
	// Path to the database file on disk.
	Path string

	// Pointer to the opened file.
	fp *os.File

	// The B-Tree structure used by the database.
	tree BTree

	// Memory mapping information.
	mmap struct {

		// Current file size in bytes.
		//
		// Can be smaller than total mapped memory.
		file int

		// Total mapped memory size in bytes.
		//
		// This can be larger than the file itself.
		total int

		// List of mmap regions.
		//
		// Each chunk is a byte slice representing
		// a mapped region of the file.
		//
		// Multiple mmaps may exist because the
		// database grows over time.
		//
		// They may not be contiguous in memory.
		chunks [][]byte
	}

	// Page management information.
	page struct {

		// Number of flushed pages already stored
		// permanently in the database file.
		//
		// Example:
		// If flushed = 10,
		// pages 0-9 already exist on disk.
		flushed uint64

		// Newly allocated pages not yet flushed
		// to disk.
		//
		// These temporarily live in RAM first.
		temp [][]byte
	}
}

// Initializes the memory map with at least the size of the file.
func mmapInit(fp *os.File) (int, []byte, error) {
	// Get information about the file (size, permissions, etc.)
	fi, err := fp.Stat()
	if err != nil {
		return 0, nil, fmt.Errorf("stat: %w", err)
	}

	// Make sure the file size is aligned to page boundaries.
	// A B-Tree stores data in fixed-size pages, so the file
	// must be an exact multiple of BTREE_PAGE_SIZE.
	//
	// Example:
	// If page size = 4096 bytes:
	// Valid sizes:
	//   4096
	//   8192
	//   12288
	//
	// Invalid:
	//   5000
	if fi.Size()%BTREE_PAGE_SIZE != 0 {
		return 0, nil, errors.New("File size is not a multiple of page size")
	}

	// Initial mmap region size.
	//
	// 64 << 20 means:
	//   64 shifted left by 20 bits
	//
	// Bit shifting left by 20 is equivalent to:
	//   64 * (2^20)
	//
	// 2^20 = 1,048,576 (1 MB)
	//
	// So:
	//   64 << 20
	// = 64 * 1,048,576
	// = 67,108,864 bytes
	// = 64 MB
	//
	// This is just a fast low-level way to write:
	//   64 * 1024 * 1024
	mmapSize := 64 << 20

	// Ensure mmap size is page aligned too.
	// Memory mapping works best when aligned to pages.
	if mmapSize%BTREE_PAGE_SIZE != 0 {
		panic("mmapSize is not a multiple of page size")
	}

	// If the file is larger than 64 MB,
	// keep doubling mmapSize until it fits.
	//
	// Example:
	// File size = 100 MB
	//
	// 64 MB -> too small
	// 128 MB -> enough
	for mmapSize < int(fi.Size()) {
		mmapSize *= 2
	}

	// mmapSize can be larger than the actual file.
	//
	// That's okay because mmap reserves virtual memory space.
	// The file itself is still only fi.Size() bytes.

	// Create a memory mapping.
	//
	// This maps the file directly into memory so you can
	// access file contents like a normal byte slice.
	//
	// Instead of:
	//   read(file)
	//   write(file)
	//
	// You can do:
	//   chunk[0] = 10
	//   fmt.Println(chunk[100])
	//
	// and it automatically interacts with the file.
	chunk, err := syscall.Mmap(
		// File descriptor (integer ID used by OS)
		int(fp.Fd()),

		// Offset in file where mapping starts.
		// 0 = start from beginning of file.
		0,

		// Total bytes to map into memory.
		mmapSize,

		// Memory protection flags:
		//
		// PROT_READ  -> can read memory
		// PROT_WRITE -> can modify memory
		syscall.PROT_READ|syscall.PROT_WRITE,

		// MAP_SHARED means:
		// Changes to mapped memory are written back
		// to the actual file and visible to other processes.
		//
		// Alternative:
		// MAP_PRIVATE -> changes stay local only
		syscall.MAP_SHARED,
	)

	if err != nil {
		return 0, nil, fmt.Errorf("mmap: %w", err)
	}

	// Return:
	//
	// 1. Actual file size
	// 2. Memory-mapped byte slice
	// 3. nil error
	return int(fi.Size()), chunk, nil
}

// Checks if current mmap size is enough and extends if not.
func extendMmap(db *KV, npages int) error {

	// Calculate required bytes.
	required := npages * BTREE_PAGE_SIZE

	// If current mmap size is already large enough,
	// no need to extend.
	if db.mmap.total >= required {
		return nil
	}

	// Create another mmap region.
	chunk, err := syscall.Mmap(

		// File descriptor
		int(db.fp.Fd()),

		// Offset in file where mapping starts
		int64(db.mmap.total),

		// Number of bytes to map
		db.mmap.total,

		// Read/write permissions
		syscall.PROT_READ|syscall.PROT_WRITE,

		// Shared mapping
		syscall.MAP_SHARED,
	)

	if err != nil {
		return fmt.Errorf("mmap: %w", err)
	}

	// Double total mapped size.
	db.mmap.total += db.mmap.total

	// Store the newly created mmap chunk.
	db.mmap.chunks = append(db.mmap.chunks, chunk)

	return nil
}

// Gets a page from the database using a page number.
//
// ptr = page number
//
// Returns:
//
//	BNode -> wrapper around one page of bytes
func (db *KV) pageGet(ptr uint64) BNode {

	// start tracks the starting page number
	// of the current mmap chunk.
	//
	// Example:
	//
	// chunk 0:
	// pages 0-16383
	//
	// chunk 1:
	// pages 16384-32767
	start := uint64(0)

	// Iterate through every mmap chunk.
	//
	// Each chunk is a large byte slice returned by mmap.
	for _, chunk := range db.mmap.chunks {

		// Calculate ending page number for this chunk.
		//
		// len(chunk) gives bytes.
		//
		// Divide by page size to get number of pages.
		//
		// Example:
		// len(chunk) = 64 MB
		// page size = 4096
		//
		// pages = 16384
		end := start + uint64(len(chunk))/BTREE_PAGE_SIZE

		// Check if requested page exists in this chunk.
		if ptr < end {

			// Calculate byte offset INSIDE this mmap chunk.
			//
			// Example:
			//
			// ptr = 20
			// start = 16
			//
			// page index inside chunk:
			//   20 - 16 = 4
			//
			// byte offset:
			//   4 * 4096
			offset := BTREE_PAGE_SIZE * (ptr - start)

			// Slice out exactly one page.
			//
			// chunk[offset : offset+BTREE_PAGE_SIZE]
			//
			// creates a view into the mmap memory.
			//
			// No copying happens here.
			return BNode{
				chunk[offset : offset+BTREE_PAGE_SIZE],
			}
		}

		// Move to next chunk's page range.
		start = end
	}

	// If no chunk contains this page number
	panic("bad pointer")
}

// Loads the master page from the database.
//
// Master Page Layout:
//
// | signature | root ptr | pages used |
// |   16B     |    8B    |     8B     |

// Bytes 0-15   -> database signature
// Bytes 16-23  -> root page pointer
// Bytes 24-31  -> total pages used
func masterLoad(db *KV) error {

	// no master page exists yet
	// database has never been initialized
	if db.mmap.total == 0 {

		// Reserve page 0 for the future master page.
		//
		// flushed = number of pages already allocated.
		//
		// Reserve:
		// page 0 = master page
		db.page.flushed = 1

		return nil
	}

	// Get the first mmap chunk.
	data := db.mmap.chunks[0]

	// Read root page pointer from bytes 16-23.
	root := binary.LittleEndian.Uint64(data[16:])

	// Read total pages used from bytes 24-31.
	used := binary.LittleEndian.Uint64(data[24:])

	// Verify database signature.
	if !bytes.Equal([]byte(DB_SIG), data[:16]) {
		return errors.New("Bad signature")
	}

	// Validate master page values.
	//
	// Checks:
	//
	// 1. used pages must be at least 1
	//    because master page exists
	//
	// 2. used pages cannot exceed actual file size
	//
	// Example:
	// file supports 100 pages
	// but master page says 500 pages, master page is corrupted
	bad := !(1 <= used &&
		used <= uint64(db.mmap.file/BTREE_PAGE_SIZE))

	// Validate root pointer.
	//
	// Root page must exist within allocated pages.
	//
	// Invalid:
	// root = 9999
	// used = 10
	bad = bad || !(root < used)

	if bad {
		return errors.New("Bad master page")
	}

	// Store loaded metadata into database object.
	db.tree.root = root
	db.page.flushed = used

	return nil
}

// Updates the master page on disk.
func masterStore(db *KV) error {

	// Create a fixed 32-byte buffer. See master page format.
	var data [32]byte

	// Copy database signature into bytes 0-15.
	copy(data[:16], []byte(DB_SIG))

	// Store root page pointer into bytes 16-23.
	binary.LittleEndian.PutUint64(
		data[16:],
		db.tree.root,
	)

	// Store total allocated pages into bytes 24-31.
	binary.LittleEndian.PutUint64(
		data[24:],
		db.page.flushed,
	)

	// IMPORTANT:
	//
	// Writing through mmap is NOT atomic.
	//
	// Atomic means:
	// either the whole write succeeds
	// or none of it happens.
	//
	// If crash/power loss happens during mmap write,
	// the master page could become partially updated.
	//
	// So instead:
	// use pwrite() syscall.
	//
	// pwrite writes directly to the file at a specific offset.

	_, err := syscall.Pwrite(

		// File descriptor
		int(db.fp.Fd()),

		// Bytes to write
		data[:],

		// Offset in file
		//
		// 0 means:
		// write at beginning of file
		// where master page lives.
		0,
	)

	if err != nil {
		return fmt.Errorf(
			"write master page: %w",
			err,
		)
	}

	return nil
}

// Allocates a new temporary database page
// and returns its page number.
func (db *KV) pageNew(node BNode) uint64 {

	// Ensure node fits within one page.
	if len(node.data) > BTREE_PAGE_SIZE {
		panic("bad node")
	}

	// Calculate page number for new page.
	//
	// flushed:
	// pages already stored on disk
	//
	// temp:
	// newly allocated pages in RAM
	//
	// Example:
	// flushed = 10
	// len(temp) = 2
	//
	// new page becomes:
	// page 12
	ptr := db.page.flushed +
		uint64(len(db.page.temp))

	// Store page temporarily in memory.
	//
	// Not flushed to disk yet.
	db.page.temp = append(
		db.page.temp,
		node.data,
	)

	// Return logical page number.
	return ptr
}

// Deletes a page.
//
// Currently unimplemented.
func (db *KV) pageDel(uint64) {
	// TODO:
	// add free list / page reuse
}

// Extends the physical database file
// so it can hold at least `npages` pages.
func extendFile(db *KV, npages int) error {

	// Current number of pages file supports.
	filePages := db.mmap.file /
		BTREE_PAGE_SIZE

	// Already large enough.
	if filePages >= npages {
		return nil
	}

	// Grow file gradually.
	//
	// Avoids extending file every insert.
	for filePages < npages {

		// Grow by 1/8 current size.
		//
		// Example:
		// 80 -> +10
		// 800 -> +100
		inc := max(filePages/8, 1)

		filePages += inc
	}

	// Convert page count -> bytes.
	fileSize := filePages *
		BTREE_PAGE_SIZE

	// Allocate physical disk space.
	err := syscall.Fallocate(

		// File descriptor
		int(db.fp.Fd()),

		// Flags
		0,

		// Starting offset
		0,

		// Final file size
		int64(fileSize),
	)

	if err != nil {
		return fmt.Errorf(
			"fallocate: %w",
			err,
		)
	}

	// Update tracked file size.
	db.mmap.file = fileSize

	return nil
}
