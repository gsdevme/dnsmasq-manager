package health

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"
)

// headerLen is the size of a DNS message header (RFC 1035 section 4.1.1).
const headerLen = 12

// versionBindQuestion is the question section for version.bind, type TXT (16),
// class CH (3). dnsmasq answers it locally, without asking upstream servers.
const versionBindQuestion = "\x07version\x04bind\x00\x00\x10\x00\x03"

// probe sends one version.bind query over UDP to addr and waits for a reply
// with the same ID and the QR bit set. Any response code counts as a reply.
// It gives up at the earlier of timeout and the end of ctx.
func probe(ctx context.Context, addr string, timeout time.Duration) error {
	if addr == "" {
		return errors.New("probe address not set")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", addr)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()

	id, query, err := versionBindQuery()
	if err != nil {
		return err
	}
	if _, err := conn.Write(query); err != nil {
		return fmt.Errorf("send query to %s: %w", addr, err)
	}

	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("no reply from %s: %w", addr, ctxErr)
		}
		return fmt.Errorf("no reply from %s: %w", addr, err)
	}
	return checkReply(buf[:n], id)
}

// versionBindQuery builds a standard query for version.bind with a random ID,
// recursion not desired and one question.
func versionBindQuery() (uint16, []byte, error) {
	var idBytes [2]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return 0, nil, fmt.Errorf("query ID: %w", err)
	}
	id := binary.BigEndian.Uint16(idBytes[:])
	q := make([]byte, headerLen, headerLen+len(versionBindQuestion))
	binary.BigEndian.PutUint16(q[0:2], id)
	binary.BigEndian.PutUint16(q[4:6], 1)
	return id, append(q, versionBindQuestion...), nil
}

// checkReply reports whether msg is a DNS response to the query with the
// given ID: a full header, the matching ID and the QR bit set.
func checkReply(msg []byte, id uint16) error {
	if len(msg) < headerLen {
		return fmt.Errorf("reply is %d bytes, shorter than a DNS header", len(msg))
	}
	if got := binary.BigEndian.Uint16(msg[0:2]); got != id {
		return fmt.Errorf("reply ID %d does not match query ID %d", got, id)
	}
	if msg[2]&0x80 == 0 {
		return errors.New("reply is not a DNS response (QR bit clear)")
	}
	return nil
}
