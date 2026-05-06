package code

import (
	"fmt"
	"testing"
)

// helper to check consistency between B-tree and reference map
func assertEqual(t *testing.T, c *C) {
	for k, v := range c.ref {
		got, ok := c.tree.Get([]byte(k))
		if !ok {
			t.Fatalf("missing key in B-tree: %s", k)
		}
		if string(got) != v {
			t.Fatalf("value mismatch for key %s: got=%s want=%s", k, string(got), v)
		}
	}

	// also ensure no extra keys exist in B-tree
	// (optional depending on your Get implementation)
}

// ----------------------
// Basic Insert Test
// ----------------------
func TestInsertBasic(t *testing.T) {
	c := newC()

	c.add("a", "1")
	c.add("b", "2")
	c.add("c", "3")

	assertEqual(t, c)
}

// ----------------------
// Overwrite Test
// ----------------------
func TestOverwrite(t *testing.T) {
	c := newC()

	c.add("key", "1")
	c.add("key", "2") // overwrite

	assertEqual(t, c)
}

// ----------------------
// Many Inserts (forces splits)
// ----------------------
func TestInsertMany(t *testing.T) {
	c := newC()

	for i := range 1000 {
		k := fmt.Sprintf("key_%d", i)
		v := fmt.Sprintf("val_%d", i)
		c.add(k, v)
	}

	assertEqual(t, c)
}

// ----------------------
// Delete Test
// ----------------------
func TestDelete(t *testing.T) {
	c := newC()

	for i := range 100 {
		c.add(fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i))
	}

	for i := range 50 {
		c.del(fmt.Sprintf("k%d", i))
	}

	assertEqual(t, c)
}

// ----------------------
// Delete Non-existent
// ----------------------
func TestDeleteNonExistent(t *testing.T) {
	c := newC()

	ok := c.del("does_not_exist")
	if ok {
		t.Fatalf("expected delete to return false")
	}
}

// ----------------------
// Mixed Operations
// ----------------------
func TestMixedOperations(t *testing.T) {
	c := newC()

	for i := range 200 {
		c.add(fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i))
	}

	for i := range 100 {
		c.del(fmt.Sprintf("k%d", i))
	}

	for i := 50; i < 150; i++ {
		c.add(fmt.Sprintf("k%d", i), "updated")
	}

	assertEqual(t, c)
}
