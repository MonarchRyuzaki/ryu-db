package storage

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

/*
Slotted Page Memory Layout:
+----------------+------------------------------------------+
| Header         | Magic(4), Checksum(4), PageID(4),        |
| (32 bytes)     | LSN(8), PageType(1), SlotCount(2),       |
|                | FreeSpaceOffset(2), Reserved(7)          |
+----------------+------------------------------------------+
| Slot Directory | Slot 0 Offset (2)                        |
| (Grows Down)   | Slot 1 Offset (2)                        |
|                | ...                                      |
+----------------+------------------------------------------+
| Free Space     |                                          |
|                | (Unallocated bytes)                      |
|                |                                          |
+----------------+------------------------------------------+
| Cell Data      | ...                                      |
| (Grows Up)     | Cell 1 Data                              |
|                | Cell 0 Data                              |
+----------------+------------------------------------------+
*/

// Note: Slot directory is currently unsorted (insertion order).
// A sorted index array can be maintained separately for binary
// search within a page without changing slot IDs. Deferred.

const (
	PageSize   = 4096
	HeaderSize = 32
	Magic      = 0xDAB0BA55 // Read as "DABO BASS"
)

// Header Offsets
const (
	magicOffset     = 0  // 4 bytes
	checksumOffset  = 4  // 4 bytes
	pageIDOffset    = 8  // 4 bytes
	lsnOffset       = 12 // 8 bytes (Log Sequence Number)
	pageTypeOffset  = 20 // 1 byte  (LeafNode=1, InternalNode=2)
	slotCountOffset = 21 // 2 bytes
	freeSpaceOffset = 23 // 2 bytes
	// 25-31: Reserved (7 bytes)
)

const (
	SlotSize = 4 // 2 bytes for cell offset, 2 bytes for cell size.
)

const (
	PageTypeMeta     uint8 = 0
	PageTypeLeaf     uint8 = 1
	PageTypeInternal uint8 = 2
	PageTypeOverflow uint8 = 3
	PageTypeFree     uint8 = 4
)

const MetaPageID uint32 = 0

type Page struct {
	data []byte // Raw 4096 byte slice
}

func NewPage(mode uint8) *Page {
	p := &Page{
		data: make([]byte, PageSize),
	}
	p.Init(mode)
	return p
}

func (p *Page) Init(mode uint8) {
	binary.LittleEndian.PutUint32(p.data[magicOffset:], Magic)
	p.setFreeSpaceOffset(uint16(PageSize))
	p.setSlotCount(0)
	p.SetLSN(0)
	p.SetPageType(mode)
}

// --- Header Accessors ---

func (p *Page) VerifyMagic() bool {
	return binary.LittleEndian.Uint32(p.data[magicOffset:]) == Magic
}

func (p *Page) SetChecksum() {
	binary.LittleEndian.PutUint32(p.data[checksumOffset:], 0)
	sum := crc32.ChecksumIEEE(p.data)
	binary.LittleEndian.PutUint32(p.data[checksumOffset:], sum)
}

func (p *Page) VerifyChecksum() bool {
	stored := binary.LittleEndian.Uint32(p.data[checksumOffset:])
	binary.LittleEndian.PutUint32(p.data[checksumOffset:], 0)
	actual := crc32.ChecksumIEEE(p.data)
	binary.LittleEndian.PutUint32(p.data[checksumOffset:], stored)
	return stored == actual
}

func (p *Page) GetPageID() uint32 {
	return binary.LittleEndian.Uint32(p.data[pageIDOffset : pageIDOffset+4])
}

func (p *Page) SetPageID(id uint32) {
	binary.LittleEndian.PutUint32(p.data[pageIDOffset:pageIDOffset+4], id)
}

func (p *Page) GetLSN() uint64 {
	return binary.LittleEndian.Uint64(p.data[lsnOffset : lsnOffset+8])
}

func (p *Page) SetLSN(lsn uint64) {
	binary.LittleEndian.PutUint64(p.data[lsnOffset:lsnOffset+8], lsn)
}

func (p *Page) GetPageType() uint8 {
	return p.data[pageTypeOffset]
}

func (p *Page) SetPageType(t uint8) {
	p.data[pageTypeOffset] = t
}

func (p *Page) getSlotCount() uint16 {
	return binary.LittleEndian.Uint16(p.data[slotCountOffset : slotCountOffset+2])
}

func (p *Page) setSlotCount(n uint16) {
	binary.LittleEndian.PutUint16(p.data[slotCountOffset:slotCountOffset+2], n)
}

func (p *Page) getFreeSpaceOffset() uint16 {
	return binary.LittleEndian.Uint16(p.data[freeSpaceOffset : freeSpaceOffset+2])
}

func (p *Page) setFreeSpaceOffset(o uint16) {
	binary.LittleEndian.PutUint16(p.data[freeSpaceOffset:freeSpaceOffset+2], o)
}

func (p *Page) GetData() []byte {
	return p.data
}

// --- Data Operations ---

func (p *Page) Insert(cellData []byte) (uint16, error) {
	if p.GetPageType() != PageTypeInternal && p.GetPageType() != PageTypeLeaf {
		return 0, fmt.Errorf("Cant perform Insert on page type : %d", p.GetPageType());
	}
	slotCount := p.getSlotCount()
	freeOffset := p.getFreeSpaceOffset()

	cellSize := uint16(len(cellData))
	neededSpace := cellSize + SlotSize

	directoryEnd := HeaderSize + (slotCount * SlotSize)

	if uint16(directoryEnd)+neededSpace > freeOffset {
		return 0, errors.New("page full")
	}

	newFreeOffset := freeOffset - cellSize
	copy(p.data[newFreeOffset:freeOffset], cellData)
	p.setFreeSpaceOffset(newFreeOffset)

	slotEntryOffset := HeaderSize + (slotCount * SlotSize)
	binary.LittleEndian.PutUint16(p.data[slotEntryOffset:slotEntryOffset+2], newFreeOffset)
	binary.LittleEndian.PutUint16(p.data[slotEntryOffset+2:slotEntryOffset+4], cellSize)

	newSlotID := slotCount
	p.setSlotCount(slotCount + 1)

	return newSlotID, nil
}

func (p *Page) Get(slotID uint16) ([]byte, error) {
	if p.GetPageType() != PageTypeInternal && p.GetPageType() != PageTypeLeaf {
		return nil, fmt.Errorf("Cant perform Get on page type : %d", p.GetPageType());
	}
	slotCount := p.getSlotCount()
	if slotID >= slotCount {
		return nil, errors.New("invalid slot id")
	}

	slotEntryOffset := HeaderSize + (slotID * SlotSize)
	cellOffset := binary.LittleEndian.Uint16(p.data[slotEntryOffset : slotEntryOffset+2])
	cellSize := binary.LittleEndian.Uint16(p.data[slotEntryOffset+2 : slotEntryOffset+4])

	return p.data[cellOffset : cellOffset+cellSize], nil
}

// --- Overflow Page Operations ---

const overflowNextPageOffset = HeaderSize // 32
const overflowDataOffset = 36
const MaxOverflowDataSize = PageSize - overflowDataOffset // 4060

func (p *Page) GetNextOverflowPageID() uint32 {
	return binary.LittleEndian.Uint32(p.data[overflowNextPageOffset : overflowNextPageOffset+4])
}

func (p *Page) SetNextOverflowPageID(id uint32) {
	binary.LittleEndian.PutUint32(p.data[overflowNextPageOffset:overflowNextPageOffset+4], id)
}

func (p *Page) WriteOverflowData(data []byte) {
	copy(p.data[overflowDataOffset:], data)
}

func (p *Page) ReadOverflowData(length uint32) []byte {
	// Prevent reading out of bounds
	if length > MaxOverflowDataSize {
		length = MaxOverflowDataSize
	}
	return p.data[overflowDataOffset : overflowDataOffset+length]
}

// --- Meta Page Operations ---
const metaRootPageOffset = HeaderSize // 32
const metaFirstFreePageOffset = 36

func (p *Page) GetRootPageID() uint32 {
	return binary.LittleEndian.Uint32(p.data[metaRootPageOffset : metaRootPageOffset+4])
}

func (p *Page) SetRootPageID(id uint32) {
	binary.LittleEndian.PutUint32(p.data[metaRootPageOffset:metaRootPageOffset+4], id)
}

func (p *Page) GetFirstFreePageID() uint32 {
	return binary.LittleEndian.Uint32(p.data[metaFirstFreePageOffset : metaFirstFreePageOffset+4])
}

func (p *Page) SetFirstFreePageID(id uint32) {
	binary.LittleEndian.PutUint32(p.data[metaFirstFreePageOffset:metaFirstFreePageOffset+4], id)
}

// --- Free Page Operations ---
const freeNextPageOffset = HeaderSize // 32

func (p *Page) GetNextFreePageID() uint32 {
	return binary.LittleEndian.Uint32(p.data[freeNextPageOffset : freeNextPageOffset+4])
}

func (p *Page) SetNextFreePageID(id uint32) {
	binary.LittleEndian.PutUint32(p.data[freeNextPageOffset:freeNextPageOffset+4], id)
}
