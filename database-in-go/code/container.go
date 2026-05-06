package code

import "unsafe"

// C = container / test harness for the B-tree
// Simulates a disk using an in-memory map
type C struct {
	tree  BTree             // the B-tree itself
	ref   map[string]string // reference map (ground truth for testing)
	pages map[uint64]BNode  // "disk": pageID → node
}

// newC initializes the container and wires up the BTree callbacks
func newC() *C {
	pages := map[uint64]BNode{} // simulate disk pages

	return &C{
		tree: BTree{
			get: func(u uint64) BNode {
				// retrieve node from "disk" using page ID
				node, ok := pages[u]
				if !ok {
					panic("error retrieving node")
				}
				return node
			},
			new: func(b BNode) uint64 {
				// ensure node fits within page size
				if b.nbytes() > BTREE_PAGE_SIZE {
					panic("node size exceeds max page size")
				}

				// generate a "unique" key using memory address
				key := uint64(uintptr(unsafe.Pointer(&b.data[0])))

				// ensure no collision
				if pages[key].data != nil {
					panic("pointer already allocated")
				}

				// store node in "disk"
				pages[key] = b
				return key
			},
			del: func(u uint64) {
				// delete node from "disk"
				_, ok := pages[u]
				if !ok {
					panic("error retrieving node")
				}
				delete(pages, u)
			},
		},
		ref:   map[string]string{}, // reference map for correctness checking
		pages: pages,               // shared "disk"
	}
}

// add inserts into both:
// 1. B-tree
// 2. reference map
func (c *C) add(key string, val string) {
	c.tree.Insert([]byte(key), []byte(val))
	c.ref[key] = val
}

// del removes key from both structures
func (c *C) del(key string) bool {
	delete(c.ref, key)                // remove from reference map
	return c.tree.Delete([]byte(key)) // remove from B-tree
}
