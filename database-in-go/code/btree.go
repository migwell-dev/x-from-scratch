package code

import (
	"bytes"
	"encoding/binary"
)

type BNode struct {
	data []byte // array of bytes so we can dump onto disk
}

const (
	BNODE_NODE = 1 // internal nodes without values
	BNODE_LEAF = 2 // leaf nodes with values
)

// can't use in-memory pointers since nodes are stored on disk pages
type BTree struct {
	// pointer (non-zero page number)
	root uint64
	// callbacks for managing on-disk pages
	get func(uint64) BNode // dereference a pointer (disk page to node)
	new func(BNode) uint64 // allocate a new page
	del func(uint64)       // deallocate a page
}

const HEADER = 4 // reserve 4 bytes for header

// constraints
const BTREE_PAGE_SIZE = 4096 // 4kb page size
const BTREE_MAX_KEY_SIZE = 1000
const BTREE_MAX_VAL_SIZE = 3000

func init() {
	node1max := HEADER + 8 + 2 + 4 + BTREE_MAX_KEY_SIZE + BTREE_MAX_VAL_SIZE
	if node1max > BTREE_PAGE_SIZE {
		panic("node size exceeds page size")
	}
}

// DECODING B-TREE NODES

// header

// btype reads the first 2 bytes of the node header.
// These 2 bytes store the "node type" (leaf or internal).
func (node BNode) btype() uint16 {
	// bytes [0:2] = node type
	return binary.LittleEndian.Uint16(node.data[0:2])
}

// nkeys reads the next 2 bytes of the header.
// These 2 bytes store how many keys are currently in the node.
func (node BNode) nkeys() uint16 {
	// bytes [2:4] = number of keys in this node
	return binary.LittleEndian.Uint16(node.data[2:4])
}

// setHeader writes the node metadata into the first 4 bytes:
//
// Layout of first 4 bytes:
// [0:2] -> node type (leaf or internal)
// [2:4] -> number of keys in the node
func (node BNode) setHeader(btype uint16, nkeys uint16) {
	// write node type into bytes [0:2]
	binary.LittleEndian.PutUint16(node.data[0:2], btype)

	// write number of keys into bytes [2:4]
	binary.LittleEndian.PutUint16(node.data[2:4], nkeys)
}

// POINTERS

// getPtr returns the child pointer (page ID) at position idx.
//
// Each pointer is stored as 8 bytes (uint64), so we compute its position
// inside the byte array using: HEADER + (8 * idx)
func (node BNode) getPtr(idx uint16) uint64 {
	if idx >= node.nkeys() {
		panic("index out of range")
	}

	// layout:
	// [ HEADER ][ ptr0 ][ ptr1 ][ ptr2 ] ...
	//
	// each pointer = 8 bytes (uint64)
	pos := HEADER + 8*idx

	// read 8 bytes starting at computed position
	return binary.LittleEndian.Uint64(node.data[pos:])
}

// setPtr writes a child pointer (page ID) at position idx.
//
// Each pointer takes 8 bytes, so we compute its offset using 8 * idx
func (node BNode) setPtr(idx uint16, val uint64) {
	if idx >= node.nkeys() {
		panic("index out of range")
	}

	// compute byte offset of the pointer inside the node
	pos := HEADER + 8*idx

	// write 8-byte uint64 pointer into that position
	binary.LittleEndian.PutUint64(node.data[pos:], val)
}

// OFFSET LIST

// offsetPos computes where the "offset table entry" for a given key index lives.
//
// A B-tree node is laid out in memory like this:
//
// [ HEADER ][ PTRS ][ OFFSET TABLE ][ KEY+VALUE DATA ... ]
//
// HEADER          = metadata (type, number of keys)
// PTRS            = child pointers (8 bytes each)
// OFFSET TABLE    = 2 bytes per key (points into key/value area)
// KEY+VALUE DATA  = actual stored data
//
// This function returns the byte position of the OFFSET entry for key idx.
func offsetPos(node BNode, idx uint16) uint16 {
	if idx < 1 || idx > node.nkeys() {
		panic("index out of range")
	}

	// Layout breakdown:
	//
	// HEADER        = fixed metadata size
	// 8 * nkeys     = space for all child pointers
	// 2 * (idx - 1) = offset entry for key idx (2 bytes per entry)
	//
	// Offset 0 is not stored in the list as it always == 0.
	// Visualization:
	//
	// [HEADER][PTR0][PTR1]...[PTRn][OFF1][OFF2][OFF3]...
	//                              ^
	//                         we index here (2 bytes each)
	return HEADER + 8*node.nkeys() + 2*(idx-1)
}

// getOffset returns where the actual key/value data starts (byte offset).
//
// The offset table stores:
//
//	offset[i] = start position of key i in the data section
func (node BNode) getOffset(idx uint16) uint16 {
	if idx == 0 {
		return 0
	}

	// Read 2-byte offset from the offset table
	//
	// Visualization:
	//
	// OFFSET TABLE:
	//   idx=0 -> [ 0:2 ] (not stored)
	//   idx=1 -> [ 2:4 ]
	//   idx=2 -> [ 4:6 ]
	//
	// Each entry tells us where the key/value begins in the DATA section.
	return binary.LittleEndian.Uint16(node.data[offsetPos(node, idx):])
}

// setOffset writes the starting position of a key/value pair into the offset table.
//
// Meaning:
//
//	"Key i starts at byte X inside the data section"
func (node BNode) setOffset(idx uint16, offset uint16) {
	// Write 2-byte offset into the correct slot in the offset table
	binary.LittleEndian.PutUint16(
		node.data[offsetPos(node, idx):],
		offset,
	)
}

// KEY-VALUE

// kvPos returns the byte position where the KV (key-value) entry starts.
//
// Node layout:
// [ HEADER ][ PTRS ][ OFFSETS ][ KV DATA ... ]
//
// KV DATA is a packed byte region containing:
//
//	[klen][vlen][key bytes][value bytes]
//
// So kvPos(idx) = start of KV section + offset[idx]
func (node BNode) kvPos(idx uint16) uint16 {
	if idx > node.nkeys() {
		panic("index out of range")
	}

	// Skip fixed sections:
	// HEADER        → metadata
	// 8*nkeys       → pointer array (uint64 each)
	// 2*nkeys       → offset array (uint16 each)
	// then jump into KV region using offset table
	return HEADER +
		8*node.nkeys() +
		2*node.nkeys() +
		node.getOffset(idx)
}

// getKey returns the key at index idx.
//
// KV layout at kvPos:
// [0:2]   → key length (klen)
// [2:4]   → value length (vlen)
// [4:]    → key bytes
func (node BNode) getKey(idx uint16) []byte {
	if idx > node.nkeys() {
		panic("index out of range")
	}

	pos := node.kvPos(idx)

	// read key length (first 2 bytes of KV entry)
	keyLen := binary.LittleEndian.Uint16(node.data[pos:])

	// key starts after header (4 bytes total metadata inside KV entry)
	// layout: [klen(2)][vlen(2)][key...]
	start := pos + 4
	return node.data[start : start+keyLen]
}

// getVal returns the value at index idx.
//
// KV layout:
// [0:2]   → key length
// [2:4]   → value length
// [4:]    → key bytes
// [4+klen:] → value bytes
func (node BNode) getVal(idx uint16) []byte {
	if idx > node.nkeys() {
		panic("index out of range")
	}

	pos := node.kvPos(idx)

	// read key/value lengths
	keyLen := binary.LittleEndian.Uint16(node.data[pos+0:])
	valLen := binary.LittleEndian.Uint16(node.data[pos+2:])

	// value starts after:
	// 4 bytes header + key bytes (skip 4 bytes and the length of key)
	start := pos + 4 + keyLen
	return node.data[start : start+valLen]
}

// NODE SIZE

// nbytes returns the total number of bytes used by this node
//
// It computes the end position of the last key-value pair
func (node BNode) nbytes() uint16 {
	if node.nkeys() == 0 {
		return HEADER
	}

	pos := node.kvPos(node.nkeys())

	klen := binary.LittleEndian.Uint16(node.data[pos+0:])
	vlen := binary.LittleEndian.Uint16(node.data[pos+2:])

	return pos + 4 + klen + vlen
}

// INSERTION

// Returns the position of candidate index for key
func nodeLookup(node BNode, key []byte) uint16 {
	nkeys := node.nkeys()
	found := uint16(0)

	// Note:
	// key[0] is a "copy" of parent separator, so we start at 1

	for i := uint16(1); i < nkeys; i++ {
		cmp := bytes.Compare(node.getKey(i), key)

		// if current key <= target, update candidate position
		if cmp <= 0 {
			found = i
		}

		// if current key >= target, stop searching
		if cmp >= 0 {
			break
		}
	}

	// returns index where key should go (or closest smaller key)
	return found
}

// Modifies a new BNode of type leaf with one extra key and inserts new KV pair
//
// Pattern: copy-insert-copy
func leafInsert(
	new BNode, old BNode, idx uint16,
	key []byte, val []byte,
) {
	// new node will have one extra key
	new.setHeader(BNODE_LEAF, old.nkeys()+1)

	// copy keys BEFORE insertion point
	nodeAppendRange(new, old, 0, 0, idx)

	// insert new key-value at idx
	nodeAppendKV(new, idx, 0, key, val)

	// copy keys AFTER insertion point
	nodeAppendRange(new, old, idx+1, idx, old.nkeys()-idx)
}

// Copies keys from old node to new node
func nodeAppendRange(
	new BNode, old BNode,
	dstNew uint16, srcOld uint16, n uint16,
) {
	if srcOld+n > old.nkeys() {
		panic("index out of range")
	}
	if dstNew+n > new.nkeys() {
		panic("index out of range")
	}

	if n == 0 {
		return
	}

	// copy pointers
	for i := uint16(0); i < n; i++ {
		new.setPtr(dstNew+i, old.getPtr(srcOld+i))
	}

	// copy offsets
	// offsets must be recalculated because data position changes

	dstBegin := new.getOffset(dstNew)
	srcBegin := old.getOffset(srcOld)

	for i := uint16(1); i <= n; i++ {
		// shift offsets relative to new position
		offset := dstBegin + old.getOffset(srcOld+i) - srcBegin
		new.setOffset(dstNew+i, offset)
	}

	// copy raw KV bytes
	begin := old.kvPos(srcOld)
	end := old.kvPos(srcOld + n)

	copy(new.data[new.kvPos(dstNew):], old.data[begin:end])
}

// Appends KV to Node
func nodeAppendKV(
	new BNode, idx uint16,
	ptr uint64, key []byte, val []byte,
) {
	// set pointer
	new.setPtr(idx, ptr)

	// write KV entry
	pos := new.kvPos(idx)

	// write lengths
	binary.LittleEndian.PutUint16(new.data[pos+0:], uint16(len(key)))
	binary.LittleEndian.PutUint16(new.data[pos+2:], uint16(len(val)))

	// write key bytes
	copy(new.data[pos+4:], key)

	// write value bytes
	copy(new.data[pos+4+uint16(len(key)):], val)

	// update offset table
	// next key starts after this KV entry
	new.setOffset(
		idx+1,
		new.getOffset(idx)+4+uint16(len(key)+len(val)),
	)
}

// Inserts KV into a node, the result may be split into 2 nodes
//
// The caller is responsible for deallocating the input node,
// and splitting and allocating the result nodes.
func treeInsert(tree *BTree, node BNode, key []byte, val []byte) BNode {
	// the result node
	// allowed to be bigger than 1 page and will be split if so.
	new := BNode{data: make([]byte, 2*BTREE_PAGE_SIZE)}

	// index to insert the key
	idx := nodeLookup(node, key)

	// switch on node type
	switch node.btype() {
	case BNODE_LEAF:
		// leaf, node.getKey(idx) <= key
		if bytes.Equal(key, node.getKey(idx)) {
			// found key, update it
			leafInsert(new, node, idx, key, val)
		} else {
			// insert after position
			leafInsert(new, node, idx+1, key, val)
		}
	case BNODE_NODE:
		// internal node, insert into child node
		nodeInsert(tree, new, node, idx, key, val)
	default:
		panic("bad node with no valid type")
	}

	return new
}

// Deallocates old child and inserts new node
//
// Adjusts for splits
func nodeInsert(
	tree *BTree, new BNode, node BNode, idx uint16,
	key []byte, val []byte,
) {
	// Follow pointer at index idx to child node
	kptr := node.getPtr(idx)
	knode := tree.get(kptr)

	// Delete old child (immutability: we will replace it)
	tree.del(kptr)

	// Recursively insert into that child
	knode = treeInsert(tree, knode, key, val)

	// Split child if needed (may return 1, 2, or 3 nodes)
	nsplit, splitted := nodeSplit3(knode)

	// Replace the old child with new split nodes
	nodeReplaceKidN(tree, new, node, idx, splitted[:nsplit]...)
}

// Splits a node into two nodes (left, right)
//
// Guaranteed that the 2nd node fits within page size
func nodeSplit2(left BNode, right BNode, old BNode) {
	nkeys := old.nkeys()

	// split roughly in half
	mid := nkeys / 2

	// assign key counts
	left.setHeader(old.btype(), mid)
	right.setHeader(old.btype(), nkeys-mid)

	// copy first half → left
	nodeAppendRange(left, old, 0, 0, mid)

	// copy second half → right
	nodeAppendRange(right, old, 0, mid, nkeys-mid)

	// safety: right must fit in one page
	if right.nbytes() > BTREE_PAGE_SIZE {
		panic("node exceeds page size")
	}
}

// Splits a node, may return 1-3 nodes
func nodeSplit3(old BNode) (uint16, [3]BNode) {

	// case 1: already fits -> no split
	if old.nbytes() <= BTREE_PAGE_SIZE {
		old.data = old.data[:BTREE_PAGE_SIZE]
		return 1, [3]BNode{old}
	}

	// case 2: split into 2 nodes
	left := BNode{make([]byte, 2*BTREE_PAGE_SIZE)} // extra space
	right := BNode{make([]byte, BTREE_PAGE_SIZE)}

	nodeSplit2(left, right, old)

	// if left fits, we’re done
	if left.nbytes() <= BTREE_PAGE_SIZE {
		left.data = left.data[:BTREE_PAGE_SIZE]
		return 2, [3]BNode{left, right}
	}

	// case 3: left still too big -> split again
	leftleft := BNode{make([]byte, BTREE_PAGE_SIZE)}
	middle := BNode{make([]byte, BTREE_PAGE_SIZE)}

	nodeSplit2(leftleft, middle, left)

	// safety check
	if leftleft.nbytes() > BTREE_PAGE_SIZE {
		panic("maximum splits reached")
	}

	return 3, [3]BNode{leftleft, middle, right}
}

// Replaces one child pointer in a parent node
// with multiple new child nodes (result of a split).
func nodeReplaceKidN(
	tree *BTree, new BNode, old BNode, idx uint16,
	kids ...BNode,
) {
	inc := uint16(len(kids)) // number of new child nodes

	// new node has:
	// old keys + (new children - 1)
	new.setHeader(BNODE_NODE, old.nkeys()+inc-1)

	// copy everything BEFORE the replaced child
	nodeAppendRange(new, old, 0, 0, idx)

	// insert new children (from split)
	for i, node := range kids {
		nodeAppendKV(
			new,
			idx+uint16(i),
			tree.new(node), // allocate page for child
			node.getKey(0), // separator key
			nil,            // internal nodes don’t store values
		)
	}

	// copy everything AFTER the replaced child
	nodeAppendRange(
		new,
		old,
		idx+inc,
		idx+1,
		old.nkeys()-(idx+1),
	)
}

// DELETION

// Removes a key from a leaf node
//
// Takes in a new node and copies keys [:idx] and [idx+1:]
func leafDelete(new BNode, old BNode, idx uint16) {
	new.setHeader(old.btype(), old.nkeys()-1)
	nodeAppendRange(new, old, 0, 0, idx)
	nodeAppendRange(new, old, idx, idx+1, old.nkeys()-(idx+1))
}

func nodeDelete(tree *BTree, node BNode, idx uint16, key []byte) BNode {
	// recurse into child node
	kptr := node.getPtr(idx)
	updated := treeDelete(tree, tree.get(kptr), key)
	if len(updated.data) == 0 {
		return BNode{} // not found
	}
	tree.del(kptr)

	new := BNode{data: make([]byte, BTREE_PAGE_SIZE)}
	// check for merging
	mergeDir, sibling := shouldMerge(tree, node, idx, updated)
	switch {
	case mergeDir < 0: // left
		merged := BNode{data: make([]byte, BTREE_PAGE_SIZE)}
		nodeMerge(merged, sibling, updated)
		tree.del(node.getPtr(idx - 1))
		nodeReplace2Kid(new, node, idx-1, tree.new(merged), merged.getKey(0))
	case mergeDir > 0:
		merged := BNode{data: make([]byte, BTREE_PAGE_SIZE)}
		nodeMerge(merged, updated, sibling)
		tree.del(node.getPtr(idx + 1))
		nodeReplace2Kid(new, node, idx, tree.new(merged), merged.getKey(0))
	case mergeDir == 0:
		if updated.nkeys() <= 0 {
			panic("no keys in node")
		}
		nodeReplaceKidN(tree, new, node, idx, updated)
	}
	return new
}

func nodeReplace2Kid(
	new BNode, node BNode,
	idx uint16, ptr uint64, key []byte,
) {
	new.setHeader(node.btype(), node.nkeys()-1)
	nodeAppendRange(new, node, 0, 0, idx)
	nodeAppendKV(new, idx, ptr, key, nil)
	nodeAppendRange(
		new,
		node,
		idx+1,                // destination (shifted left by 1)
		idx+2,                // source (skip two old children)
		node.nkeys()-(idx+2), // number of remaining entries
	)
}

func nodeMerge(new BNode, left BNode, right BNode) {
	new.setHeader(left.btype(), left.nkeys()+right.nkeys())
	nodeAppendRange(new, left, 0, 0, left.nkeys())
	nodeAppendRange(new, right, left.nkeys(), 0, right.nkeys())
}

func shouldMerge(
	tree *BTree, node BNode,
	idx uint16, updated BNode,
) (int, BNode) {
	// arbitrary condition to check node size for merging
	if updated.nbytes() > BTREE_PAGE_SIZE/4 {
		return 0, BNode{}
	}

	if idx > 0 {
		sibling := tree.get(node.getPtr(idx - 1))
		merged := sibling.nbytes() + updated.nbytes() - HEADER
		if merged <= BTREE_PAGE_SIZE {
			return -1, sibling
		}
	}

	if idx+1 < node.nkeys() {
		sibling := tree.get(node.getPtr(idx + 1))
		merged := sibling.nbytes() + updated.nbytes() - HEADER
		if merged <= BTREE_PAGE_SIZE {
			return +1, sibling
		}
	}
	return 0, BNode{}
}

func treeDelete(tree *BTree, node BNode, key []byte) BNode {
	idx := nodeLookup(node, key)

	switch node.btype() {
	case BNODE_LEAF:
		if !bytes.Equal(key, node.getKey(idx)) {
			return BNode{} // not found
		}
		// delete the key in the leaf
		new := BNode{data: make([]byte, BTREE_PAGE_SIZE)}
		leafDelete(new, node, idx)
		return new
	case BNODE_NODE:
		return nodeDelete(tree, node, idx, key)
	default:
		panic("node with invalid type")
	}

}

func (tree *BTree) Delete(key []byte) bool {
	if len(key) == 0 || len(key) > BTREE_MAX_KEY_SIZE {
		panic("invalid key length")
	}
	if tree.root == 0 {
		return false
	}

	updated := treeDelete(tree, tree.get(tree.root), key)
	if len(updated.data) == 0 {
		return false // not found
	}

	tree.del(tree.root)
	if updated.btype() == BNODE_NODE && updated.nkeys() == 1 {
		// remove a level
		tree.root = updated.getPtr(0)
	} else {
		tree.root = tree.new(updated)
	}
	return true
}

func (tree *BTree) Get(key []byte) ([]byte, bool) {
	if tree.root == 0 {
		return nil, false
	}

	node := tree.get(tree.root)

	for {
		idx := nodeLookup(node, key)

		switch node.btype() {

		case BNODE_LEAF:
			// check if key actually exists at idx
			if idx < node.nkeys() && bytes.Equal(node.getKey(idx), key) {
				return node.getVal(idx), true
			}
			return nil, false

		case BNODE_NODE:
			// follow pointer to child node
			ptr := node.getPtr(idx)
			node = tree.get(ptr)

		default:
			panic("unknown node type")
		}
	}
}

func (tree *BTree) Insert(key []byte, val []byte) {
	if len(key) == 0 ||
		len(key) > BTREE_MAX_KEY_SIZE ||
		len(val) > BTREE_MAX_VAL_SIZE {
		panic("invalid size for key or val")
	}

	if tree.root == 0 {
		// create first node
		root := BNode{data: make([]byte, BTREE_PAGE_SIZE)}
		root.setHeader(BNODE_LEAF, 2)
		// dummy key to make the tree balanced
		nodeAppendKV(root, 0, 0, nil, nil)
		nodeAppendKV(root, 1, 0, key, val)
		tree.root = tree.new(root)
	}

	node := tree.get(tree.root)
	tree.del(tree.root)

	// insert recursively
	node = treeInsert(tree, node, key, val)
	nsplit, splitted := nodeSplit3(node)
	if nsplit > 1 {
		// root was split, add next level
		root := BNode{data: make([]byte, BTREE_PAGE_SIZE)}
		root.setHeader(BNODE_NODE, nsplit)
		for i, knode := range splitted[:nsplit] {
			ptr, key := tree.new(knode), knode.getKey(0)
			nodeAppendKV(root, uint16(i), ptr, key, nil)
		}
		tree.root = tree.new(root)
	} else {
		tree.root = tree.new(splitted[0])
	}
}
