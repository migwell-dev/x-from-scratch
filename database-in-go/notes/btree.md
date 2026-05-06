# B-Tree Code Review

---

## Node Memory Layout

Every node serialized flat byte array. Mental model:

```
[ HEADER (4B) ][ POINTERS (8B each) ][ OFFSET TABLE (2B each) ][ KV DATA ... ]
```

```
Bytes:  0    2    4         4+8*nkeys        4+8*n+2*n
        |TYPE|NKEY|PTR0|PTR1|...|OFF1|OFF2|...|kv0|kv1|...|
```

**KV entry format** (inside KV DATA region):
```
[klen: 2B][vlen: 2B][key bytes][val bytes]
```

**Key rule:** offset[0] = 0 always (not stored). offset[i] = byte distance from KV region start to key i.

---

## Constants & Types

```go
BNODE_NODE = 1   // internal node (has child pointers, no values)
BNODE_LEAF = 2   // leaf node (has values, no real child pointers)

HEADER           = 4      // bytes reserved for type + nkeys
BTREE_PAGE_SIZE  = 4096   // max node size (1 disk page)
BTREE_MAX_KEY_SIZE = 1000
BTREE_MAX_VAL_SIZE = 3000
```

**BNode** — just a `[]byte`. No pointers. Disk-safe.

**BTree** — holds:
- `root uint64` — page number of root node
- `get(uint64) BNode` — load page from disk
- `new(BNode) uint64` — write node, return page number
- `del(uint64)` — free a page

**`init()`** — sanity check at startup. Panics if max possible node exceeds page size.

---

## Header Functions

### `btype() uint16`
Reads bytes `[0:2]`. Returns `BNODE_NODE` or `BNODE_LEAF`.

### `nkeys() uint16`
Reads bytes `[2:4]`. Returns how many keys live in this node.

### `setHeader(btype, nkeys uint16)`
Writes both into `[0:4]`. Call this first when building a new node.

---

## Pointer Functions
*(Only meaningful for internal nodes. Leaves store 0 here.)*

### `getPtr(idx) uint64`
Returns child page number at position `idx`.

```
offset = HEADER + 8*idx
```

### `setPtr(idx, val)`
Writes child page number at position `idx`. Same offset formula.

---

## Offset Table Functions

The offset table maps key index → byte position inside KV region.  
offset[0] = 0 always (implicit). offset[1..n] stored in table.

### `offsetPos(node, idx) uint16`
Returns byte position of offset table entry for key `idx`.

```go
offsetPos = HEADER + 8*nkeys + 2*(idx-1)
```

Visual:
```go
[HEADER][PTR0..PTRn][OFF1][OFF2][OFF3]...
                     ^ idx=1 starts here
```

### `getOffset(idx) uint16`
Returns stored offset for key `idx`. If `idx == 0`, returns 0 (hardcoded).

### `setOffset(idx, offset)`
Writes offset value into table slot for key `idx`.

---

## KV Access Functions

### `kvPos(idx) uint16`
Returns absolute byte position of KV entry `idx` inside node's data.

```
kvPos = HEADER + 8*nkeys + 2*nkeys + getOffset(idx)
      = HEADER + 10*nkeys + offset[idx]
```

Visual — jumping into KV region:
```
[HEADER][PTRS][OFFSETS][  KV DATA  ]
                        ^ kvPos(0)
                              ^ kvPos(1)
```

### `getKey(idx) []byte`
1. Jump to `kvPos(idx)`
2. Read `klen` from `[pos:pos+2]`
3. Return `data[pos+4 : pos+4+klen]`

### `getVal(idx) []byte`
1. Jump to `kvPos(idx)`
2. Read `klen` from `[pos:pos+2]`, `vlen` from `[pos+2:pos+4]`
3. Return `data[pos+4+klen : pos+4+klen+vlen]`

### `nbytes() uint16`
Total bytes used by node. Finds last KV entry and reads past it:
```
pos = kvPos(nkeys)    // position of last entry
return pos + 4 + klen + vlen
```

---

## Lookup

### `nodeLookup(node, key) uint16`
Binary search (linear scan) for insertion position.

- Starts at `i=1` (index 0 = copied parent separator, skip it)
- Tracks last index where `node.getKey(i) <= key`
- Stops when `node.getKey(i) >= key`
- Returns best candidate index

**Returns:** largest `i` where `key[i] <= target`. Caller inserts at `i` or `i+1`.

---

## Append Helpers

### `nodeAppendKV(new, idx, ptr, key, val)`
Writes one KV entry into node at position `idx`:
1. `setPtr(idx, ptr)`
2. Write `klen`, `vlen`, key bytes, val bytes at `kvPos(idx)`
3. Update offset table: `offset[idx+1] = offset[idx] + 4 + klen + vlen`

### `nodeAppendRange(new, old, dstNew, srcOld, n)`
Copies `n` keys from `old` (starting at `srcOld`) into `new` (starting at `dstNew`).

Steps:
1. Copy `n` pointers
2. Recalculate and copy offsets (shift by `dstBegin - srcBegin`)
3. Copy raw KV bytes with `copy()`

**Why recalculate offsets?** KV data lands at different position in new node, so all offsets shift by a constant delta.

---

## Leaf Insert

### `leafInsert(new, old, idx, key, val)`
Pattern: **copy-before → insert → copy-after**

```
new = [ old[0..idx-1] | NEW KV | old[idx..end] ]
```

1. `new.setHeader(BNODE_LEAF, old.nkeys()+1)`
2. `nodeAppendRange(new, old, 0, 0, idx)` — copy keys before
3. `nodeAppendKV(new, idx, 0, key, val)` — insert new key
4. `nodeAppendRange(new, old, idx+1, idx, old.nkeys()-idx)` — copy rest

---

## Tree Insert

### `treeInsert(tree, node, key, val) BNode`
Top-level insert. Returns new node (may be oversized, caller splits).

- Allocates `2*PAGE_SIZE` buffer (safe for oversized node)
- Calls `nodeLookup` to find position
- **If leaf:** call `leafInsert` at `idx` (update) or `idx+1` (new key)
- **If internal:** call `nodeInsert` to recurse into child

### `nodeInsert(tree, new, node, idx, key, val)`
Handles insertion into internal node:

1. `kptr = node.getPtr(idx)` — get child page number
2. `knode = tree.get(kptr)` — load child from disk
3. `tree.del(kptr)` — free old child page (immutability)
4. `knode = treeInsert(tree, knode, key, val)` — recurse
5. `nsplit, splited = nodeSplit3(knode)` — split result if oversized
6. `nodeReplaceKidN(...)` — write new children into parent

---

## Splitting

### `nodeSplit2(left, right, old)`
Splits `old` into two nodes at midpoint.

```
mid = nkeys / 2
left  = old[0 .. mid-1]
right = old[mid .. end]
```

Panics if `right > PAGE_SIZE` (right must always fit).

### `nodeSplit3(old) (uint16, [3]BNode)`
Decides how many splits needed:

| Case | Condition | Result |
|------|-----------|--------|
| 1 | `nbytes <= PAGE_SIZE` | No split, return 1 node |
| 2 | Left fits after split | Return 2 nodes |
| 3 | Left still too big | Split left again, return 3 nodes |

Max output = 3 nodes. If 3 splits not enough → panic.

### `nodeReplaceKidN(tree, new, old, idx, kids...)`
Replaces one child pointer in parent with `len(kids)` new children.

```
new = [ old[0..idx-1] | kid0 | kid1 | ... | old[idx+1..end] ]
```

1. `new.setHeader(BNODE_NODE, old.nkeys() + len(kids) - 1)`
2. Copy keys before `idx`
3. For each new kid: `tree.new(kid)` to allocate page, then `nodeAppendKV` with kid's first key as separator
4. Copy keys after `idx`

**Why `old.nkeys() + inc - 1`?** One old child replaced by `inc` new children. Net change = `inc - 1`.

---

## Insert Flow Summary

```
treeInsert(root, key, val)
│
├─ nodeLookup → find idx
│
├─ LEAF? → leafInsert (copy-insert-copy pattern)
│
└─ INTERNAL? → nodeInsert
    │
    ├─ load child at idx
    ├─ delete old child page
    ├─ recurse: treeInsert(child, key, val)
    ├─ nodeSplit3 → 1, 2, or 3 result nodes
    └─ nodeReplaceKidN → update parent pointers
```

---

## Offset Math Cheat Sheet

| Section | Byte Position |
|---------|--------------|
| Node type | `[0:2]` |
| Key count | `[2:4]` |
| Pointer `i` | `HEADER + 8*i` |
| Offset entry `i` | `HEADER + 8*nkeys + 2*(i-1)` |
| KV entry `i` | `HEADER + 8*nkeys + 2*nkeys + offset[i]` |
| Key bytes at `i` | `kvPos(i) + 4` |
| Val bytes at `i` | `kvPos(i) + 4 + klen` |

