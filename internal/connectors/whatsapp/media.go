package whatsapp

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"strconv"
)

// LF-04: media travel through evolution-go as base64 inside JSON, both ways. They are decoded and
// encoded as streams here, so a file of gigabytes never sits in memory: what is held is a buffer of a
// few KB, and the JSON around the media, which is bounded.

// Errors of the media stream.
var (
	errNoContent     = errors.New("downloadmedia: no content")
	errInvalidJSON   = errors.New("downloadmedia: invalid response")
	errEnvelope      = errors.New("downloadmedia: the JSON around the media is too large")
	errInvalidBase64 = errors.New("media: invalid base64")
)

// maxKey bounds a JSON key compared by the scanner; a longer key is read and matches nothing.
const maxKey = 64

// mediaContent returns the decoded content of a /message/downloadmedia answer read from r: the base64
// string at "data", or at "data"."base64", which evolution-go sends as either. The string is unescaped,
// stripped of a data: URL prefix and decoded as it is read, never held whole; the JSON before it is
// bounded by maxJSON bytes (errEnvelope over it) and what follows it is not read. An answer without
// the string, or with an empty one, is errNoContent.
func mediaContent(r io.Reader, maxJSON int64) (io.Reader, error) {
	s := &jsonScanner{br: bufio.NewReaderSize(r, 32<<10), left: maxJSON}
	str, err := s.findContent()
	if err != nil {
		return nil, err
	}
	return decodeBase64(str)
}

// decodeBase64 decodes base64 read from r, after a data: URL prefix ("data:image/png;base64,") when it
// starts with one; corrupt input fails the read with errInvalidBase64.
func decodeBase64(r io.Reader) (io.Reader, error) {
	r, err := stripDataURLStream(r)
	if err != nil {
		return nil, err
	}
	return &base64Errors{r: base64.NewDecoder(base64.StdEncoding, r)}, nil
}

// maxDataURLHead bounds the "data:<type>;base64," prefix of a data URL.
const maxDataURLHead = 512

// stripDataURLStream is stripDataURL on a stream: a value that starts with "data:" goes on after its
// ";base64,"; one that has none within maxDataURLHead bytes is no base64.
func stripDataURLStream(r io.Reader) (io.Reader, error) {
	head := make([]byte, len("data:"))
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	if n < len(head) || string(head) != "data:" {
		return io.MultiReader(bytes.NewReader(head[:n]), r), nil
	}
	var one [1]byte
	for read := n; read < maxDataURLHead; read++ {
		if _, err := io.ReadFull(r, one[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil, errInvalidBase64
			}
			return nil, err
		}
		head = append(head, one[0])
		if bytes.HasSuffix(head, []byte(";base64,")) {
			return r, nil
		}
	}
	return nil, errInvalidBase64
}

// base64Errors words a corrupt base64 input as errInvalidBase64; other errors (a cut answer, a cap
// of the client) go through as they are.
type base64Errors struct{ r io.Reader }

func (b *base64Errors) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	var corrupt base64.CorruptInputError
	if errors.As(err, &corrupt) {
		err = errInvalidBase64
	}
	return n, err
}

// jsonScanner walks a JSON answer without building it, for the one string it is after. left is how
// many more bytes of JSON it may read outside that string.
type jsonScanner struct {
	br   *bufio.Reader
	left int64
}

func (s *jsonScanner) next() (byte, error) {
	if s.left <= 0 {
		return 0, errEnvelope
	}
	c, err := s.br.ReadByte()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return 0, errInvalidJSON
		}
		return 0, err
	}
	s.left--
	return c, nil
}

func (s *jsonScanner) back() {
	_ = s.br.UnreadByte()
	s.left++
}

// token is the next byte that is not white space.
func (s *jsonScanner) token() (byte, error) {
	for {
		c, err := s.next()
		if err != nil {
			return 0, err
		}
		switch c {
		case ' ', '\t', '\n', '\r':
			continue
		}
		return c, nil
	}
}

// findContent finds the media string of the answer and returns a reader of its unescaped content.
func (s *jsonScanner) findContent() (io.Reader, error) {
	if c, err := s.token(); err != nil {
		return nil, err
	} else if c != '{' {
		return nil, errInvalidJSON
	}
	return s.members(func(key string, c byte) (io.Reader, bool, error) {
		if key != "data" {
			return nil, false, nil
		}
		switch c {
		case '"':
			r, err := s.content()
			return r, true, err
		case '{':
			r, err := s.members(func(key string, c byte) (io.Reader, bool, error) {
				if key != "base64" || c != '"' {
					return nil, false, nil
				}
				r, err := s.content()
				return r, true, err
			})
			return r, true, err
		}
		return nil, true, errNoContent
	})
}

// members walks the members of the object whose '{' was read: visit sees each key with the first byte
// of its value and says whether it took the value (and what it found); a value it did not take is
// skipped. The end of the object without a find is errNoContent.
func (s *jsonScanner) members(visit func(key string, first byte) (io.Reader, bool, error)) (io.Reader, error) {
	for {
		c, err := s.token()
		if err != nil {
			return nil, err
		}
		switch c {
		case '}':
			return nil, errNoContent
		case ',':
			continue
		case '"':
		default:
			return nil, errInvalidJSON
		}
		key, err := s.key()
		if err != nil {
			return nil, err
		}
		if c, err := s.token(); err != nil {
			return nil, err
		} else if c != ':' {
			return nil, errInvalidJSON
		}
		first, err := s.token()
		if err != nil {
			return nil, err
		}
		r, took, err := visit(key, first)
		if took || err != nil {
			return r, err
		}
		if err := s.skip(first); err != nil {
			return nil, err
		}
	}
}

// key reads a key whose opening quote was read; one longer than maxKey comes back cut, and matches
// none of the keys looked for.
func (s *jsonScanner) key() (string, error) {
	var b []byte
	for {
		c, err := s.next()
		if err != nil {
			return "", err
		}
		switch c {
		case '"':
			return string(b), nil
		case '\\':
			if _, err := s.next(); err != nil { // an escaped key matches nothing looked for
				return "", err
			}
			c = 0
		}
		if len(b) <= maxKey {
			b = append(b, c)
		}
	}
}

// skip skips the value whose first byte is first.
func (s *jsonScanner) skip(first byte) error {
	switch first {
	case '"':
		return s.skipString()
	case '{', '[':
		depth := 1
		for depth > 0 {
			c, err := s.next()
			if err != nil {
				return err
			}
			switch c {
			case '"':
				if err := s.skipString(); err != nil {
					return err
				}
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
		return nil
	}
	for { // a number, true, false or null: up to the byte that ends it
		c, err := s.next()
		if err != nil {
			return err
		}
		switch c {
		case ',', '}', ']', ' ', '\t', '\n', '\r':
			s.back()
			return nil
		}
	}
}

func (s *jsonScanner) skipString() error {
	for {
		c, err := s.next()
		if err != nil {
			return err
		}
		switch c {
		case '"':
			return nil
		case '\\':
			if _, err := s.next(); err != nil {
				return err
			}
		}
	}
}

// content is the reader of the string whose opening quote was read; an empty string is errNoContent.
// Its bytes are not counted as JSON: the client's cap and the blob store's limit bound them.
func (s *jsonScanner) content() (io.Reader, error) {
	if c, err := s.br.Peek(1); err == nil && c[0] == '"' {
		return nil, errNoContent
	}
	return &jsonString{br: s.br}, nil
}

// jsonString reads the content of a JSON string from br, unescaped, up to its closing quote.
type jsonString struct {
	br   *bufio.Reader
	done bool
}

func (j *jsonString) Read(p []byte) (int, error) {
	if j.done {
		return 0, io.EOF
	}
	n := 0
	for n < len(p) {
		if _, err := j.br.Peek(1); err != nil {
			if n > 0 {
				return n, nil
			}
			if errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			return 0, err
		}
		chunk, _ := j.br.Peek(j.br.Buffered())
		i := bytes.IndexAny(chunk, "\"\\")
		if i != 0 {
			if i < 0 {
				i = len(chunk)
			}
			k := copy(p[n:], chunk[:i])
			_, _ = j.br.Discard(k)
			n += k
			continue
		}
		if chunk[0] == '"' {
			_, _ = j.br.Discard(1)
			j.done = true
			if n == 0 {
				return 0, io.EOF
			}
			return n, nil
		}
		c, err := j.escape()
		if err != nil {
			return n, err
		}
		p[n] = c
		n++
	}
	return n, nil
}

// escape reads an escape sequence at the reader's position. Base64 holds ASCII only: a \u escape of
// anything else is no base64.
func (j *jsonString) escape() (byte, error) {
	seq, err := j.br.Peek(2)
	if err != nil {
		return 0, unexpected(err)
	}
	var c byte
	size := 2
	switch seq[1] {
	case '"', '\\', '/':
		c = seq[1]
	case 'b':
		c = '\b'
	case 'f':
		c = '\f'
	case 'n':
		c = '\n'
	case 'r':
		c = '\r'
	case 't':
		c = '\t'
	case 'u':
		size = 6
		seq, err = j.br.Peek(6)
		if err != nil {
			return 0, unexpected(err)
		}
		v, perr := strconv.ParseUint(string(seq[2:6]), 16, 16)
		if perr != nil || v >= 0x80 {
			return 0, errInvalidBase64
		}
		c = byte(v)
	default:
		return 0, errInvalidJSON
	}
	_, _ = j.br.Discard(size)
	return c, nil
}

func unexpected(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}

// base64Len is the length of the base64 (with padding) of n bytes.
func base64Len(n int64) int64 { return (n + 2) / 3 * 4 }

// base64Reader encodes what it reads from r as base64 (with padding), as it is read: the pull side of
// base64.NewEncoder, so a request body needs neither a goroutine nor the file in memory.
type base64Reader struct {
	r   io.Reader
	raw []byte
	enc []byte
	out []byte // encoded bytes not read yet
	eof bool
}

// newBase64Reader returns the base64 of r, encoded in blocks of 48 KB.
func newBase64Reader(r io.Reader) *base64Reader {
	const block = 3 << 14
	return &base64Reader{r: r, raw: make([]byte, block), enc: make([]byte, base64.StdEncoding.EncodedLen(block))}
}

func (b *base64Reader) Read(p []byte) (int, error) {
	for len(b.out) == 0 {
		if b.eof {
			return 0, io.EOF
		}
		// Whole blocks of 3 bytes until the end: padding comes only after the last one.
		n, err := io.ReadFull(b.r, b.raw)
		switch {
		case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
			b.eof = true
		case err != nil:
			return 0, err
		}
		base64.StdEncoding.Encode(b.enc, b.raw[:n])
		b.out = b.enc[:base64.StdEncoding.EncodedLen(n)]
	}
	n := copy(p, b.out)
	b.out = b.out[n:]
	return n, nil
}
