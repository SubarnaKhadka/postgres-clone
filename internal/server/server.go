package server

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"gopostgres/internal/ast"
	"gopostgres/internal/catalog"
	"gopostgres/internal/executor"
	"gopostgres/internal/parser"
	"gopostgres/internal/protocol"
	"gopostgres/internal/txn"
	"gopostgres/internal/types"
)

type Server struct {
	listener   net.Listener
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	activeConn atomic.Int64
	nextConnID atomic.Int32
	config     Config
	catalog    *catalog.Catalog
	txnManager *txn.TxnManager
}

type Config struct {
	Port           int // default 5432
	MaxConnections int // default 100
}

func NewServer(config Config, cat *catalog.Catalog) *Server {
	ctx, cancel := context.WithCancel(context.Background())

	return &Server{
		config:     config,
		catalog:    cat,
		ctx:        ctx,
		cancel:     cancel,
		txnManager: txn.NewTxnManager(),
	}
}

func (s *Server) Start() error {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", s.config.Port))
	if err != nil {
		return err
	}

	s.listener = listener
	slog.Info("Server is listening on tcp", "port", s.config.Port)

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			// Server is shutting down - context is cancelled
			if s.ctx.Err() != nil {
				return nil
			}

			// Temporary error (too many open files) - backoff and rery
			// if error is network error and if it represent timeout error
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				slog.Warn("temporary accept error retrying", "error", err)
				time.Sleep(5 * time.Millisecond)
				continue
			}
			// Fatal error- can't be recover
			return fmt.Errorf("accept error: %w", err)
		}

		// Connection limit check
		if s.activeConn.Load() >= int64(s.config.MaxConnections) {
			slog.Warn("Connection limit reached", "Max", s.config.MaxConnections)
			if err := conn.Close(); err != nil {
				slog.Error("Failed to close connection", "error", err)
			}
			continue
		}

		s.wg.Add(1)
		go s.handleConnection(conn)
	}
}

func txnStatusByte(state txn.TxnState) byte {
	switch state {
	case txn.TxnInTransaction:
		return 'T'
	case txn.TxnFailed:
		return 'E'
	default:
		return 'I'
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("Panic in connection handler", "error", r)
		}
	}()

	defer func() {
		if err := conn.Close(); err != nil {
			slog.Error("Failed to close connection", "error", err)
		}
	}()

	defer s.wg.Done()

	s.activeConn.Add(1)
	defer s.activeConn.Add(-1)

	slog.Info("new connection", "remote", conn.RemoteAddr().String())
	connID := s.nextConnID.Add(1)

	startupMessage, err := protocol.ReadStartupMessage(conn)
	if err != nil {
		slog.Error("Failed to read startup message", "error", err)
		return
	}

	slog.Info("reading parameters",
		"user",
		startupMessage.Parameters["user"],
		"database",
		startupMessage.Parameters["database"],
	)

	if err := protocol.WriteAuthenticationOk(conn); err != nil {
		slog.Error("Failed to write authentication ok", "error", err)
		return
	}

	params := []struct{ key, value string }{
		{"server_version", "16.0"},
		{"client_encoding", "UTF8"},
		{"server_encoding", "UTF8"},
		{"DateStyle", "ISO, MDY"},
		{"integer_datetimes", "on"},
	}

	for _, p := range params {
		if err := protocol.WriteParameterStatus(conn, p.key, p.value); err != nil {
			slog.Error("Failed to write parameter status", "error", err)
			return
		}
	}

	if err := protocol.WriteBackendKeyData(conn, connID, rand.Int32()); err != nil {
		slog.Error("Failed to write backend key data", "error", err)
		return
	}

	if err := protocol.WriteReadyForQuery(conn, 'I'); err != nil {
		slog.Error("Failed to write ready for query", "error", err)
		return
	}

	connTxn := &txn.ConnTransaction{State: txn.TxnIdle, XID: 0}

	for {
		messageType, message, err := protocol.ReadMessage(conn)
		if err != nil {
			slog.Error("Failed to read message", "error", err)
			return
		}

		switch messageType {
		case 'Q':
			sql := string(message[:len(message)-1])
			slog.Info("Received query", "query", sql)

			p := parser.NewParser(sql)
			stmt, err := p.Parse()
			if err != nil {
				if connTxn.State == txn.TxnInTransaction {
					connTxn.State = txn.TxnFailed
				}
				protocol.WriteErrorResponse(conn, err.Error())
				protocol.WriteReadyForQuery(conn, txnStatusByte(connTxn.State))
				continue
			}

			switch stmt.(type) {
			case *ast.BeginStatement:
				if connTxn.State == txn.TxnInTransaction {
					protocol.WriteErrorResponse(conn, "WARNING: Already in transaction")
				} else {
					connTxn.XID = s.txnManager.NextXID()
					connTxn.State = txn.TxnInTransaction
				}
				protocol.WriteCommandComplete(conn, "BEGIN")
				protocol.WriteReadyForQuery(conn, txnStatusByte(connTxn.State))
				continue

			case *ast.CommitStatement:
				if connTxn.State == txn.TxnInTransaction {
					s.txnManager.Commit(connTxn.XID)
				} else if connTxn.State == txn.TxnFailed {
					s.txnManager.Abort(connTxn.XID)
				}

				connTxn.State = txn.TxnIdle
				connTxn.XID = 0
				protocol.WriteCommandComplete(conn, "COMMIT")
				protocol.WriteReadyForQuery(conn, txnStatusByte(connTxn.State))
				continue

			case *ast.RollbackStatement:
				if connTxn.State == txn.TxnInTransaction || connTxn.State == txn.TxnFailed {
					s.txnManager.Abort(connTxn.XID)
				}
				connTxn.State = txn.TxnIdle
				connTxn.XID = 0
				protocol.WriteCommandComplete(conn, "ROLLBACK")
				protocol.WriteReadyForQuery(conn, txnStatusByte(connTxn.State))
				continue
			}

			if connTxn.State == txn.TxnFailed {
				protocol.WriteErrorResponse(conn, "ERROR: Transaction failed")
				protocol.WriteReadyForQuery(conn, txnStatusByte(connTxn.State))
				continue
			}

			snapShot := s.txnManager.TakeSnapshot()
			exec := executor.NewExecutor(s.catalog, s.txnManager, connTxn.XID, snapShot)
			result, err := exec.Execute(stmt)
			if err != nil {
				if connTxn.State == txn.TxnInTransaction {
					connTxn.State = txn.TxnFailed
				}
				protocol.WriteErrorResponse(conn, err.Error())
				protocol.WriteReadyForQuery(conn, txnStatusByte(connTxn.State))
				continue
			}

			// if result has rows, send RowDescription + DataRows
			if result.Rows != nil {
				fields := make([]protocol.FieldDescription, len(result.Columns))
				for i, col := range result.Columns {
					t, _ := types.TypeByOID(col.TypeOID)

					fields[i] = protocol.FieldDescription{
						Name:     col.Name,
						TypeOID:  col.TypeOID,
						TypeSize: t.Size,
					}
				}
				protocol.WriteRowDescription(conn, fields)
				for _, row := range result.Rows {
					vals := make([]string, len(row))
					nulls := make([]bool, len(row))

					for i, v := range row {
						if v == nil {
							nulls[i] = true
						} else {
							t, _ := types.TypeByOID(result.Columns[i].TypeOID)
							encoded, _ := t.TextEncode(v)
							vals[i] = encoded
						}
					}
					protocol.WriteDataRow(conn, vals, nulls)
				}
			}
			protocol.WriteCommandComplete(conn, result.Tag)
			protocol.WriteReadyForQuery(conn, txnStatusByte(connTxn.State))

		case 'X':
			slog.Info("Client disconnected")
			return

		default:
			protocol.WriteErrorResponse(conn, "Unsupported message type")
			protocol.WriteReadyForQuery(conn, txnStatusByte(connTxn.State))
		}
	}
}

func (s *Server) Shutdown() {
	s.cancel()

	if s.listener != nil {
		if err := s.listener.Close(); err != nil {
			slog.Error("error in closing connection", "error", err)
		}
	}

	s.wg.Wait()
}
