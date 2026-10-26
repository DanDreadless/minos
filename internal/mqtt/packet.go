// Package mqtt is a small MQTT 3.1.1 client: what Minos needs to announce
// itself to Home Assistant and take commands from it, and nothing more.
//
// Supported: CONNECT with username, password and a last will; PUBLISH at
// QoS 0 (retained or not); SUBSCRIBE at QoS 0; incoming QoS 0 and 1
// messages; keep-alive pings; reconnecting with a back-off; TLS. Not
// supported, deliberately: QoS 2, persistent sessions, WebSockets and MQTT
// 5. Everything a broker sends is untrusted input — packet sizes are capped
// and every length is checked before it is used.
package mqtt

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Packet types (MQTT 3.1.1 §2.2.1).
const (
	typeConnect     = 1
	typeConnack     = 2
	typePublish     = 3
	typePuback      = 4
	typeSubscribe   = 8
	typeSuback      = 9
	typePingreq     = 12
	typePingresp    = 13
	typeDisconnect  = 14
	protocolLevel   = 4 // 3.1.1
	maxRemainingLen = 268435455
)

// MaxPacket is the largest packet the client reads. Minos only receives
// short commands and Home Assistant's status messages; anything bigger is
// skipped unread rather than buffered.
const MaxPacket = 64 << 10

var (
	errMalformed = errors.New("malformed packet")
	// errSkipped reports a packet over MaxPacket that was read past and
	// dropped; the connection is still usable.
	errSkipped = errors.New("packet too large, skipped")
)

type packet struct {
	kind  byte
	flags byte
	body  []byte
}

// readPacket reads one packet. A packet over max is discarded and reported
// as errSkipped, so a large retained message on a subscribed topic cannot
// make the client buffer it, or drop and redial in a loop.
func readPacket(r *bufio.Reader, max int) (packet, error) {
	h, err := r.ReadByte()
	if err != nil {
		return packet{}, err
	}
	n, err := readLength(r)
	if err != nil {
		return packet{}, err
	}
	p := packet{kind: h >> 4, flags: h & 0x0f}
	if n > max {
		if _, err := io.CopyN(io.Discard, r, int64(n)); err != nil {
			return packet{}, err
		}
		return p, errSkipped
	}
	p.body = make([]byte, n)
	if _, err := io.ReadFull(r, p.body); err != nil {
		return packet{}, err
	}
	return p, nil
}

// readLength decodes the variable-length "remaining length" (§2.2.3): at
// most four bytes.
func readLength(r io.ByteReader) (int, error) {
	n, shift := 0, 0
	for range 4 {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		n |= int(b&0x7f) << shift
		if b&0x80 == 0 {
			return n, nil
		}
		shift += 7
	}
	return 0, fmt.Errorf("%w: length longer than four bytes", errMalformed)
}

// encode builds a whole packet, so each one goes out in a single write.
func encode(kind, flags byte, body []byte) ([]byte, error) {
	if len(body) > maxRemainingLen {
		return nil, fmt.Errorf("packet of %d bytes is too large", len(body))
	}
	out := make([]byte, 0, len(body)+5)
	out = append(out, kind<<4|flags)
	n := len(body)
	for {
		b := byte(n & 0x7f)
		n >>= 7
		if n > 0 {
			b |= 0x80
		}
		out = append(out, b)
		if n == 0 {
			break
		}
	}
	return append(out, body...), nil
}

func appendString(b []byte, s string) []byte {
	b = binary.BigEndian.AppendUint16(b, uint16(len(s)))
	return append(b, s...)
}

// readString takes a length-prefixed string off the front of b.
func readString(b []byte) (string, []byte, error) {
	if len(b) < 2 {
		return "", nil, errMalformed
	}
	n := int(binary.BigEndian.Uint16(b))
	if len(b) < 2+n {
		return "", nil, errMalformed
	}
	return string(b[2 : 2+n]), b[2+n:], nil
}

// connectBody is CONNECT's variable header and payload (§3.1).
func connectBody(o *Options, keepAlive uint16) []byte {
	var flags byte = 0x02 // clean session
	if o.Will != nil {
		flags |= 0x04
		if o.Will.Retain {
			flags |= 0x20
		}
	}
	if o.Username != "" {
		flags |= 0x80
		if o.Password != "" {
			flags |= 0x40
		}
	}
	b := appendString(nil, "MQTT")
	b = append(b, protocolLevel, flags)
	b = binary.BigEndian.AppendUint16(b, keepAlive)
	b = appendString(b, o.ClientID)
	if o.Will != nil {
		b = appendString(b, o.Will.Topic)
		b = binary.BigEndian.AppendUint16(b, uint16(len(o.Will.Payload)))
		b = append(b, o.Will.Payload...)
	}
	if o.Username != "" {
		b = appendString(b, o.Username)
		if o.Password != "" {
			b = appendString(b, o.Password)
		}
	}
	return b
}

// connackError explains a refused CONNACK return code (§3.2.2.3).
func connackError(code byte) error {
	switch code {
	case 0:
		return nil
	case 1:
		return errors.New("the broker refused the connection: it does not speak MQTT 3.1.1")
	case 2:
		return errors.New("the broker refused the connection: client ID rejected")
	case 3:
		return errors.New("the broker refused the connection: server unavailable")
	case 4:
		return errors.New("the broker refused the connection: wrong username or password")
	case 5:
		return errors.New("the broker refused the connection: not authorized (check the username and password)")
	default:
		return fmt.Errorf("the broker refused the connection (code %d)", code)
	}
}

// publishBody encodes a QoS 0 PUBLISH (§3.3): topic, then the payload.
func publishBody(m Message) []byte {
	b := appendString(make([]byte, 0, len(m.Topic)+2+len(m.Payload)), m.Topic)
	return append(b, m.Payload...)
}

// parsePublish decodes an incoming PUBLISH. id is the packet identifier
// for QoS 1 (zero at QoS 0).
func parsePublish(p packet) (m Message, qos byte, id uint16, err error) {
	qos = (p.flags >> 1) & 0x03
	if qos > 1 {
		// Minos subscribes at QoS 0; a broker may only deliver at or below
		// the granted QoS, so QoS 2 here is a protocol violation.
		return Message{}, 0, 0, fmt.Errorf("%w: QoS %d message", errMalformed, qos)
	}
	topic, rest, err := readString(p.body)
	if err != nil {
		return Message{}, 0, 0, err
	}
	if qos == 1 {
		if len(rest) < 2 {
			return Message{}, 0, 0, errMalformed
		}
		id = binary.BigEndian.Uint16(rest)
		rest = rest[2:]
	}
	return Message{Topic: topic, Payload: rest, Retain: p.flags&0x01 != 0}, qos, id, nil
}

// subscribeBody requests each filter at QoS 0 (§3.8).
func subscribeBody(id uint16, filters []string) []byte {
	b := binary.BigEndian.AppendUint16(nil, id)
	for _, f := range filters {
		b = appendString(b, f)
		b = append(b, 0)
	}
	return b
}

// checkSuback reports a refused subscription (§3.9.3: 0x80 is failure).
func checkSuback(p packet, id uint16, filters []string) error {
	if len(p.body) < 2 || binary.BigEndian.Uint16(p.body) != id {
		return fmt.Errorf("%w: unexpected SUBACK", errMalformed)
	}
	codes := p.body[2:]
	for i, c := range codes {
		if c == 0x80 && i < len(filters) {
			return fmt.Errorf("the broker refused the subscription to %q (check the user's access rules)", filters[i])
		}
	}
	return nil
}
