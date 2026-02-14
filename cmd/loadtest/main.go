package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"time"
)

func main() {
	workers := flag.Int("w", 50, "number of concurrent workers")
	duration := flag.Duration("d", 10*time.Second, "test duration")
	port := flag.Int("p", 5432, "port")
	flag.Parse()

	addr := fmt.Sprintf("localhost:%d", *port)

	var total atomic.Int64
	var failed atomic.Int64
	var done atomic.Bool

	// print live stats every second
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		var last int64
		for range ticker.C {
			if done.Load() {
				return
			}
			current := total.Load()
			f := failed.Load()
			fmt.Printf("  conn/sec: %d  |  total: %d  |  failed: %d\n",
				current-last, current, f)
			last = current
		}
	}()

	start := time.Now()

	// stop signal
	time.AfterFunc(*duration, func() {
		done.Store(true)
	})

	// worker goroutines — each loops: connect, handshake, close, repeat
	workerDone := make(chan struct{}, *workers)
	for i := 0; i < *workers; i++ {
		go func() {
			for !done.Load() {
				if err := doConnection(addr); err != nil {
					failed.Add(1)
				} else {
					total.Add(1)
				}
			}
			workerDone <- struct{}{}
		}()
	}

	// wait for all workers
	for i := 0; i < *workers; i++ {
		<-workerDone
	}

	elapsed := time.Since(start)
	t := total.Load()
	f := failed.Load()

	fmt.Printf("\n=== Results ===\n")
	fmt.Printf("Duration:   %s\n", elapsed.Round(time.Millisecond))
	fmt.Printf("Workers:    %d\n", *workers)
	fmt.Printf("Total:      %d\n", t)
	fmt.Printf("Failed:     %d\n", f)
	fmt.Printf("Avg rate:   %.0f conn/sec\n", float64(t)/elapsed.Seconds())
}

// doConnection opens a connection, completes the handshake, sends Terminate, closes.
func doConnection(addr string) error {
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return err
	}

	if err := sendStartup(conn); err != nil {
		conn.Close()
		return err
	}

	if err := readUntilReady(conn); err != nil {
		conn.Close()
		return err
	}

	// send Terminate and close
	conn.Write([]byte{'X', 0, 0, 0, 4})
	conn.Close()
	return nil
}

func sendStartup(conn net.Conn) error {
	params := "user\x00postgres\x00database\x00postgres\x00\x00"
	length := uint32(4 + 4 + len(params))

	buf := make([]byte, length)
	binary.BigEndian.PutUint32(buf[0:4], length)
	binary.BigEndian.PutUint32(buf[4:8], 0x00030000)
	copy(buf[8:], params)

	_, err := conn.Write(buf)
	return err
}

func readUntilReady(conn net.Conn) error {
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	defer conn.SetReadDeadline(time.Time{})

	for {
		header := make([]byte, 5)
		if _, err := io.ReadFull(conn, header); err != nil {
			return err
		}

		msgType := header[0]
		length := binary.BigEndian.Uint32(header[1:5])

		if length > 4 {
			payload := make([]byte, length-4)
			if _, err := io.ReadFull(conn, payload); err != nil {
				return err
			}
		}

		if msgType == 'Z' {
			return nil
		}
	}
}
