// Package minecraft adapts itzg/minecraft-server and itzg/bungeecord
// containers, covering vanilla, Paper, and Velocity servers.
package minecraft

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// Source RCON packet types.
const (
	typeResponse    = 0
	typeCommand     = 2
	typeAuthorize   = 3
	maxPayload      = 4096
	rconDialTimeout = 5 * time.Second
	rconIOTimeout   = 8 * time.Second
)

// rconConn is a Source RCON client. The protocol is small enough that a
// dependency would cost more than it saves.
type rconConn struct {
	conn net.Conn
	next int32
}

func dialRCON(ctx context.Context, address, password string) (*rconConn, error) {
	dialer := net.Dialer{Timeout: rconDialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	r := &rconConn{conn: conn}

	// A failed auth is answered with request id -1.
	id, err := r.send(typeAuthorize, password)
	if err != nil {
		conn.Close()
		return nil, err
	}
	resID, _, err := r.receive()
	if err != nil {
		conn.Close()
		return nil, err
	}
	if resID == -1 || resID != id {
		conn.Close()
		return nil, fmt.Errorf("RCON authentication was refused")
	}
	return r, nil
}

func (r *rconConn) Close() error { return r.conn.Close() }

func (r *rconConn) Execute(command string) (string, error) {
	if _, err := r.send(typeCommand, command); err != nil {
		return "", err
	}
	_, body, err := r.receive()
	return body, err
}

func (r *rconConn) send(packetType int32, payload string) (int32, error) {
	if len(payload) > maxPayload {
		return 0, fmt.Errorf("command is too long")
	}
	r.next++
	id := r.next

	// length + id + type + payload + two terminating nulls
	size := 4 + 4 + len(payload) + 2
	buf := make([]byte, 0, size+4)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(size))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(id))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(packetType))
	buf = append(buf, payload...)
	buf = append(buf, 0, 0)

	r.conn.SetWriteDeadline(time.Now().Add(rconIOTimeout))
	_, err := r.conn.Write(buf)
	return id, err
}

func (r *rconConn) receive() (int32, string, error) {
	r.conn.SetReadDeadline(time.Now().Add(rconIOTimeout))
	reader := bufio.NewReader(r.conn)

	var size int32
	if err := binary.Read(reader, binary.LittleEndian, &size); err != nil {
		return 0, "", err
	}
	if size < 10 || size > maxPayload+16 {
		return 0, "", fmt.Errorf("RCON returned an implausible packet size")
	}

	payload := make([]byte, size)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return 0, "", err
	}

	id := int32(binary.LittleEndian.Uint32(payload[0:4]))
	body := strings.TrimRight(string(payload[8:]), "\x00")
	return id, body, nil
}

// execute opens a connection, runs one command, and closes it. Game servers
// cap concurrent RCON sessions, so holding one open per server would be a
// resource the dashboard does not need.
func execute(ctx context.Context, address, password, command string) (string, error) {
	conn, err := dialRCON(ctx, address, password)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	return conn.Execute(command)
}
