package storage

import (
	"encoding/binary"
	"fmt"
	"os"
)

const DataDir = "data/base"

func tableFilePath(tableOID uint32) string {
	return fmt.Sprintf("%s/%d", DataDir, tableOID)
}

type HeapFile struct {
	file     *os.File
	tableOID uint32
}

func NewHeapFile(tableOID uint32) (*HeapFile, error) {
	if err := os.MkdirAll(DataDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	path := tableFilePath(tableOID)
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("failed to open heap file: %w", err)
	}
	return &HeapFile{
		file:     file,
		tableOID: tableOID,
	}, nil
}

func (h *HeapFile) Close() error {
	return h.file.Close()
}

func (h *HeapFile) PageCount() (uint32, error) {
	info, err := h.file.Stat()
	if err != nil {
		return 0, err
	}
	return uint32(info.Size() / PageSize), nil
}

func (h *HeapFile) ReadPage(pageNum uint32) (*Page, error) {
	page := &Page{}
	offset := int64(pageNum) * PageSize

	_, err := h.file.ReadAt(page[:], offset)
	if err != nil {
		return nil, fmt.Errorf("failed to read page %d : %w", pageNum, err)
	}
	return page, nil
}

func (h *HeapFile) WritePage(pageNum uint32, page *Page) error {
	offset := int64(pageNum) * PageSize
	_, err := h.file.WriteAt(page[:], offset)
	if err != nil {
		return fmt.Errorf("failed to write page %d: %w", pageNum, err)
	}
	return nil
}

func (h *HeapFile) InsertTuple(data []byte) (uint32, uint16, error) {
	if len(data)+ItemPointerSize > PageSize-PageHeaderSize {
		return 0, 0, fmt.Errorf("tuple too large: %d bytes", len(data))
	}
	pageCount, err := h.PageCount()
	if err != nil {
		return 0, 0, err
	}
	// try last page
	if pageCount > 0 {
		lastPageNum := pageCount - 1
		page, err := h.ReadPage(lastPageNum)
		if err != nil {
			return 0, 0, err
		}
		itemIndex, err := page.AddTuple(data)
		if err == nil {
			if err := h.WritePage(lastPageNum, page); err != nil {
				return 0, 0, err
			}
			return lastPageNum, itemIndex, nil
		}
		// last page full, fall through to new page
	}

	// allocate a new page
	newPage := &Page{}
	newPage.InitPage()

	itemIndex, err := newPage.AddTuple(data)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to add tuple to new page: %w", err)
	}
	newPageNum := pageCount
	if err := h.WritePage(newPageNum, newPage); err != nil {
		return 0, 0, err
	}

	return newPageNum, itemIndex, nil
}

func (h *HeapFile) UpdateTupleHeader(pageNum uint32, itemIdx uint16, header *TupleHeader) error {
	page, err := h.ReadPage(pageNum)
	if err != nil {
		return err
	}

	if itemIdx >= page.GetItemCount() {
		return fmt.Errorf("item index %d out of range ", itemIdx)
	}

	pointerOffset := PageHeaderSize + itemIdx*ItemPointerSize
	tupleOffset := binary.BigEndian.Uint16(page[pointerOffset:])
	binary.BigEndian.PutUint64(page[tupleOffset:], header.Xmin)
	binary.BigEndian.PutUint64(page[tupleOffset+8:], header.Xmax)

	return h.WritePage(pageNum, page)
}
