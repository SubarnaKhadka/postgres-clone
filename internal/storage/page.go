package storage

import (
	"encoding/binary"
	"fmt"
)

const (
	PageSize        = 8192 // 8 KB
	PageHeaderSize  = 8    // simplified header
	ItemPointerSize = 4    // 2 byte offset + 2 bytes length
)

type Page [PageSize]byte

func (p *Page) GetItemCount() uint16 {
	return binary.BigEndian.Uint16(p[0:2])
}

func (p *Page) setItemCount(count uint16) {
	binary.BigEndian.PutUint16(p[0:2], count)
}

func (p *Page) GetLowerOffset() uint16 {
	return binary.BigEndian.Uint16(p[2:4])
}

func (p *Page) setLowerOffset(offset uint16) {
	binary.BigEndian.PutUint16(p[2:4], offset)
}

func (p *Page) GetUpperOffset() uint16 {
	return binary.BigEndian.Uint16(p[4:6])
}

func (p *Page) setUpperOffset(offset uint16) {
	binary.BigEndian.PutUint16(p[4:6], offset)
}

func (p *Page) InitPage() {
	for i := range p {
		p[i] = 0
	}
	p.setItemCount(0)
	p.setLowerOffset(PageHeaderSize)
	p.setUpperOffset(PageSize)
}

func (p *Page) FreeSpace() uint16 {
	return p.GetUpperOffset() - p.GetLowerOffset()
}

func (p *Page) AddTuple(data []byte) (uint16, error) {
	needed := uint16(ItemPointerSize + len(data))
	if p.FreeSpace() < needed {
		return 0, fmt.Errorf("not enough space on page")
	}

	tupleOffset := p.GetUpperOffset() - uint16(len(data))
	copy(p[tupleOffset:], data)

	itemIndex := p.GetItemCount()
	pointerOffset := PageHeaderSize + itemIndex*ItemPointerSize
	binary.BigEndian.PutUint16(p[pointerOffset:], tupleOffset)
	binary.BigEndian.PutUint16(p[pointerOffset+2:], uint16(len(data)))

	p.setItemCount(itemIndex + 1)
	p.setLowerOffset(pointerOffset + ItemPointerSize)
	p.setUpperOffset(tupleOffset)

	return itemIndex, nil
}

func (p *Page) GetTuple(index uint16) ([]byte, error) {
	if index >= p.GetItemCount() {
		return nil, fmt.Errorf("item index %d out of range", index)
	}

	pointerOffset := PageHeaderSize + index*ItemPointerSize
	tupleOffset := binary.BigEndian.Uint16(p[pointerOffset:])
	tupleLength := binary.BigEndian.Uint16(p[pointerOffset+2:])

	data := make([]byte, tupleLength)
	copy(data, p[tupleOffset:tupleOffset+tupleLength])

	return data, nil
}
