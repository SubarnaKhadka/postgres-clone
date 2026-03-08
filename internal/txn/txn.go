package txn

import (
	"slices"
	"sync"
	"sync/atomic"
)

type (
	Status   string
	TxnState int
)

const (
	TxnIdle TxnState = iota
	TxnInTransaction
	TxnFailed
)

type ConnTransaction struct {
	State TxnState
	XID   uint64
}

type Snapshot struct {
	Xmin uint64   // oldest active XID
	Xmax uint64   // next XID to be assigned
	Xip  []uint64 // in-progress XIDS
}

const (
	InProgress Status = "in_progress"
	Committed  Status = "committed"
	Aborted    Status = "aborted"
)

type TxnManager struct {
	nextXID  atomic.Uint64
	statuses map[uint64]Status
	mu       sync.RWMutex
}

func NewTxnManager() *TxnManager {
	m := &TxnManager{
		statuses: make(map[uint64]Status),
	}
	m.nextXID.Store(0)
	return m
}

func (m *TxnManager) NextXID() uint64 {
	return m.nextXID.Add(1)
}

func (m *TxnManager) SetStatus(xid uint64, status Status) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statuses[xid] = status
}

func (m *TxnManager) GetStatus(xid uint64) Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	status, exists := m.statuses[xid]
	if !exists {
		return InProgress
	}
	return status
}

func (m *TxnManager) Commit(xid uint64) {
	m.SetStatus(xid, Committed)
}

func (m *TxnManager) Abort(xid uint64) {
	m.SetStatus(xid, Aborted)
}

func (m *TxnManager) isXIDCommitted(xid uint64, snap *Snapshot) bool {
	if xid >= snap.Xmax {
		return false
	}

	found := slices.Contains(snap.Xip, xid)
	if found {
		return false
	}

	return m.GetStatus(xid) == Committed
}

func (m *TxnManager) IsVisible(xmin, xmax uint64, snap *Snapshot) bool {
	if isXminCommitted := m.isXIDCommitted(xmin, snap); !isXminCommitted {
		return false
	}

	if xmax == 0 {
		return true
	}

	if xmaxCommited := m.isXIDCommitted(xmax, snap); xmaxCommited {
		return false
	}

	return true
}

func (m *TxnManager) TakeSnapshot() *Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	xmax := m.nextXID.Load() + 1
	xmin := xmax

	inProgressXIDS := make([]uint64, 0)
	for key, value := range m.statuses {
		if value == InProgress {
			inProgressXIDS = append(inProgressXIDS, key)

			if key < xmin {
				xmin = key
			}
		}
	}

	return &Snapshot{
		Xmin: xmin,
		Xmax: xmax,
		Xip:  inProgressXIDS,
	}
}
