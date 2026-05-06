# Disk Pages (Simple Explanation)

## 1. What is a Disk Page?

A **disk page** is a fixed-size block of data stored on disk.

- Common sizes: 4KB, 8KB, 16KB
- Disk reads/writes always happen **one page at a time**

> You don’t read individual values — you read an entire page.

---

## 2. Why Pages Exist

Disks and operating systems work with **blocks**, not tiny data.

- Reading 1 byte ≈ reading the whole page
- So it’s important to **pack useful data into each page**

---

## 3. How Databases Use Pages

Data is stored as a sequence of pages:
Page 1 ... Page 4 ... Page n

Each page can contain:
- B-tree nodes
- Rows
- Index data

---

## 4. Pages in B-Trees

Instead of memory pointers:
node.left -> memory address

Use page IDs:
node.left --> pageID

Meaning:
- The child node is stored in **Page 5 on disk**

To access it:
1. Read the page from disk
2. Convert it into a node

---

## 5. Why B-Trees Use Pages

Goal: **Minimize disk I/O (slow)**

So B-trees:
- Store **many keys per node**
- Make each node fit in **one page**

Result:
- Fewer levels in the tree
- Fewer disk reads

---

## 6. Example

- Page size = 4KB
- Each key = 8 bytes

→ A single page can store **hundreds of keys**

So:
- Binary tree → many reads (deep)
- B-tree → few reads (wide and shallow)

---

## 7. Page Lifecycle

When accessing a page:

1. Request page ID
2. Check memory (cache)
3. If not found → read from disk
4. Use data
5. Write back later if modified

---

## 8. Page Cache

- OS keeps frequently used pages in memory
- Reduces disk access

→ Efficient page usage = better performance

---

## 9. Why This Matters

In a disk-based system:

- Data is stored in **pages**
- Pointers become **page IDs**
- You need a system to:
  - load pages
  - save pages

---

## Key Idea

> Disk pages are the reason B-trees are wide and shallow.

They are designed to:
- Use each page efficiently
- Reduce the number of disk reads

