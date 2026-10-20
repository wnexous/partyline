// Package devices approves new machines from the web app (DEV-01..04): `partyline setup
// --browser` creates a request and polls it with a secret; a logged-in user opens
// <instance>/devices/<code> and approves it, which creates an agent token for that user. The
// polling CLI receives the token once. Only hashes of the code and of the poll secret are
// stored, and the token stays sealed with the instance key until it is delivered.
package devices

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/secrets"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/users"
	"github.com/wnexous/partyline/internal/validation"
)

// Request codes, lifetimes and limits (DEV-01, DEV-04).
const (
	// CodeAlphabet has the letters A-Z without I and O, and the digits 2-9.
	CodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	// CodeLength is the number of characters of an approval code.
	CodeLength = 8
	// RequestTTL is how long a request can be approved.
	RequestTTL = 10 * time.Minute
	// PollInterval is the shortest time between two polls of one request.
	PollInterval = 2 * time.Second
	// CreateLimit requests per CreateWindow and client address (IPv4 address or IPv6 /64).
	CreateLimit  = 10
	CreateWindow = time.Hour
	// MaxMachineLength is the longest machine name in characters.
	MaxMachineLength = 64
	// NameSuffix follows the machine name in the name of the agent token an approval creates.
	NameSuffix = " (via setup)"
	// keep is how long requests are kept (the creation limit counts them).
	keep = CreateWindow
)

// Errors of the device endpoints.
var (
	ErrCreateLimited = httpx.NewError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited, "too many device requests from this address, try again later")
	ErrPollTooFast   = httpx.NewError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited, "polling too fast: wait 2 seconds between polls")
	ErrNotFound      = httpx.NewError(http.StatusNotFound, dto.ErrorCodeNotFound, "device request not found")
	ErrExpired       = httpx.NewError(http.StatusGone, dto.ErrorCodeGone, "device request expired")
	ErrDecided       = httpx.NewError(http.StatusConflict, dto.ErrorCodeConflict, "device request already approved or denied")
)

// Audit actions and target kind.
const (
	ActionCreate  = "device.create"
	ActionApprove = "device.approve"
	ActionDeny    = "device.deny"
	ActionDeliver = "device.deliver"
	TargetDevice  = "device"
)

// Deps are the collaborators of the Service.
type Deps struct {
	Store *store.Store
	Users *users.Service
	Box   *secrets.Box
	Audit *audit.Log
	// Now returns the current time; defaults to time.Now.
	Now func() time.Time
}

// Service manages device requests.
type Service struct {
	d Deps
}

// New returns a Service.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{d: d}
}

// Created is a new request as the CLI sees it once.
type Created struct {
	Code       string
	PollSecret string
	ExpiresAt  time.Time
}

// Request is a stored request with its current status (pending past its expiry is expired).
type Request struct {
	db.DeviceRequest
	Status dto.DeviceStatus
}

// Poll is the answer to a poll; Token is set only on the poll that delivers it.
type Poll struct {
	Status dto.DeviceStatus
	Token  string
}

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// NormalizeCode upper-cases a code and drops spaces and hyphens, as users type it.
func NormalizeCode(code string) string {
	return strings.NewReplacer(" ", "", "-", "").Replace(strings.ToUpper(strings.TrimSpace(code)))
}

func newCode() (string, error) {
	b := make([]byte, CodeLength)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = CodeAlphabet[int(b[i])%len(CodeAlphabet)] // 256 is a multiple of 32: no bias
	}
	return string(b), nil
}

// ipKey groups client addresses for the creation limit: the IPv4 address or the IPv6 /64.
func ipKey(ip netip.Addr) string {
	if !ip.IsValid() {
		return ""
	}
	ip = ip.WithZone("").Unmap()
	if ip.Is4() {
		return ip.String()
	}
	return netip.PrefixFrom(ip, 64).Masked().String()
}

func validMachine(m string) bool {
	n := utf8.RuneCountInString(m)
	if n == 0 || n > MaxMachineLength {
		return false
	}
	for _, r := range m {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// Create stores a new pending request for machine from the client address ip (DEV-01). At
// most CreateLimit requests per CreateWindow are accepted per address (DEV-04).
func (s *Service) Create(ctx context.Context, machine string, ip netip.Addr) (Created, error) {
	machine = strings.TrimSpace(machine)
	if !validMachine(machine) {
		return Created{}, &validation.Error{Fields: map[string]string{"machine": fmt.Sprintf("must have 1 to %d characters", MaxMachineLength)}}
	}
	code, err := newCode()
	if err != nil {
		return Created{}, err
	}
	secret, err := authn.NewSessionToken()
	if err != nil {
		return Created{}, err
	}
	now := s.d.Now()
	out := Created{Code: code, PollSecret: secret, ExpiresAt: now.Add(RequestTTL)}
	id := store.NewID()
	key := ipKey(ip)
	err = s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.DeleteOldDeviceRequests(ctx, now.Add(-keep).UnixMilli()); err != nil {
			return err
		}
		n, err := q.CountDeviceRequestsSince(ctx, db.CountDeviceRequestsSinceParams{IpKey: key, CreatedAt: now.Add(-CreateWindow).UnixMilli()})
		if err != nil {
			return err
		}
		if n >= CreateLimit {
			return ErrCreateLimited
		}
		ipText := ""
		if ip.IsValid() {
			ipText = ip.String()
		}
		if err := q.CreateDeviceRequest(ctx, db.CreateDeviceRequestParams{
			ID: id, CodeHash: hash(code), PollSecretHash: hash(secret), Machine: machine,
			CreatedIp: ipText, IpKey: key, CreatedAt: now.UnixMilli(), ExpiresAt: out.ExpiresAt.UnixMilli(),
		}); err != nil {
			return err
		}
		return s.d.Audit.Record(ctx, q, audit.Entry{
			Action: ActionCreate, TargetKind: TargetDevice, TargetID: id, Detail: map[string]any{"machine": machine},
		})
	})
	if err != nil {
		return Created{}, err
	}
	return out, nil
}

func (s *Service) view(row db.DeviceRequest) Request {
	st := dto.DeviceStatus(row.Status)
	if st == dto.DeviceStatusPending && s.d.Now().UnixMilli() >= row.ExpiresAt {
		st = dto.DeviceStatusExpired
	}
	return Request{DeviceRequest: row, Status: st}
}

// Get returns the request with the code for the approval page (WIZ-02).
func (s *Service) Get(ctx context.Context, code string) (Request, error) {
	row, err := s.d.Store.R().GetDeviceRequestByCode(ctx, hash(NormalizeCode(code)))
	if errors.Is(err, sql.ErrNoRows) {
		return Request{}, ErrNotFound
	}
	if err != nil {
		return Request{}, err
	}
	return s.view(row), nil
}

// Approve creates an agent token for the approving user, named after the machine, and keeps
// it sealed for the polling CLI (DEV-02). Only a pending, unexpired request can be approved.
func (s *Service) Approve(ctx context.Context, p authn.Principal, code string) (Request, error) {
	return s.decide(ctx, p, code, true)
}

// Deny refuses a pending, unexpired request (DEV-03).
func (s *Service) Deny(ctx context.Context, p authn.Principal, code string) (Request, error) {
	return s.decide(ctx, p, code, false)
}

// TokenName is the name of the token an approval creates: the machine name (cut so the name
// fits the 64-character token name limit) and NameSuffix.
func TokenName(machine string) string {
	maxMachine := 64 - utf8.RuneCountInString(NameSuffix)
	if utf8.RuneCountInString(machine) > maxMachine {
		machine = strings.TrimSpace(string([]rune(machine)[:maxMachine]))
	}
	return machine + NameSuffix
}

func (s *Service) decide(ctx context.Context, p authn.Principal, code string, approve bool) (Request, error) {
	var out Request
	err := s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		row, err := q.GetDeviceRequestByCode(ctx, hash(NormalizeCode(code)))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		switch s.view(row).Status {
		case dto.DeviceStatusExpired:
			return ErrExpired
		case dto.DeviceStatusPending:
		default:
			return ErrDecided
		}
		row.UserID = sql.NullString{String: p.UserID, Valid: true}
		action := ActionDeny
		row.Status = string(dto.DeviceStatusDenied)
		if approve {
			action = ActionApprove
			tok, secret, err := s.d.Users.CreateTokenTx(ctx, q, p.UserID, users.TokenInput{Kind: dto.TokenKindAgent, Name: TokenName(row.Machine)})
			if err != nil {
				return err
			}
			sealed, err := s.d.Box.Seal([]byte(secret))
			if err != nil {
				return err
			}
			row.Status = string(dto.DeviceStatusApproved)
			row.TokenID = sql.NullString{String: tok.ID, Valid: true}
			row.TokenOnceEnc = sealed
		}
		if err := q.DecideDeviceRequest(ctx, db.DecideDeviceRequestParams{
			Status: row.Status, UserID: row.UserID, TokenID: row.TokenID, TokenOnceEnc: row.TokenOnceEnc, ID: row.ID,
		}); err != nil {
			return err
		}
		out = s.view(row)
		return s.d.Audit.Record(ctx, q, audit.Entry{
			Action: action, TargetKind: TargetDevice, TargetID: row.ID, Detail: map[string]any{"machine": row.Machine},
		})
	})
	return out, err
}

// Poll reports the status of the request with the poll secret, at most once per
// PollInterval (DEV-04). The poll that first sees the approval returns the token and clears
// it, so it is delivered once (DEV-02).
func (s *Service) Poll(ctx context.Context, secret string) (Poll, error) {
	var out Poll
	now := s.d.Now()
	err := s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		row, err := q.GetDeviceRequestByPollSecret(ctx, hash(strings.TrimSpace(secret)))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		n, err := q.ClaimDevicePoll(ctx, db.ClaimDevicePollParams{
			Now: sql.NullInt64{Int64: now.UnixMilli(), Valid: true}, ID: row.ID,
			AllowedBefore: sql.NullInt64{Int64: now.Add(-PollInterval).UnixMilli(), Valid: true},
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrPollTooFast
		}
		out.Status = s.view(row).Status
		if out.Status != dto.DeviceStatusApproved || row.TokenOnceEnc == nil {
			return nil
		}
		token, err := s.d.Box.Open(row.TokenOnceEnc)
		if err != nil {
			return err
		}
		if err := q.TakeDeviceToken(ctx, row.ID); err != nil {
			return err
		}
		out.Token = string(token)
		return s.d.Audit.Record(ctx, q, audit.Entry{
			ActorID: row.UserID.String, Action: ActionDeliver, TargetKind: TargetDevice, TargetID: row.ID,
			Detail: map[string]any{"machine": row.Machine},
		})
	})
	return out, err
}
