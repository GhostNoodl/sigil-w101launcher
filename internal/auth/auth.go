// Package auth performs the Wizard101 login-server handshake
// (UserAuthenV3) and returns the session token the game client expects on
// its command line. This replaces the login step the official launcher UI
// performs, allowing credentials to come from the OS credential store
// instead of being typed each launch.
package auth

import (
	"context"
	"fmt"
	"time"

	crypto "github.com/cedws/w101-client-go/login"
	"github.com/cedws/w101-client-go/proto"
	"github.com/cedws/w101-proto-go/pkg/login"
)

// Session is the outcome of a successful login: everything the game client
// needs to be handed on its command line.
type Session struct {
	UserID uint64
	CK2    string
}

type Options struct {
	Server   string // login server host:port
	Username string
	Password string
	Locale   string
	// SteamPatcher sets IsSteamPatcher in the auth request. Steam-linked
	// accounts reject password logins without it (AccountClientMismatch).
	SteamPatcher bool
	DialTimeout  time.Duration
	Retries      int // the login server intermittently drops new connections
}

func (o *Options) defaults() {
	if o.Locale == "" {
		o.Locale = "English"
	}
	if o.DialTimeout <= 0 {
		o.DialTimeout = 10 * time.Second
	}
	if o.Retries <= 0 {
		o.Retries = 6
	}
}

type authenHandler struct {
	login.Service
	ch chan login.UserAuthenRsp
}

func (h *authenHandler) UserAuthenRsp(m login.UserAuthenRsp) { h.ch <- m }

// Login authenticates against the login server and returns a session.
func Login(ctx context.Context, opts Options) (*Session, error) {
	opts.defaults()

	handler := &authenHandler{ch: make(chan login.UserAuthenRsp, 1)}
	router := proto.NewMessageRouter()
	login.RegisterService(&router, handler)

	client, err := dialWithRetries(ctx, opts.Server, &router, opts.Retries, opts.DialTimeout)
	if err != nil {
		return nil, fmt.Errorf("connecting to login server: %w", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(ctx, responseTimeout)
	defer cancel()

	var (
		sid        = client.SessionID()
		timeSecs   = client.SessionTimeSecs()
		timeMillis = client.SessionTimeMillis()
	)

	// CK1 proves knowledge of the password without sending it; the result
	// is wrapped in REC1 (twofish OFB keyed by session parameters).
	ck1 := crypto.GenerateCK1(opts.Password, sid, timeSecs, timeMillis)
	token := crypto.AuthenToken(opts.Username, ck1, sid)
	rec1 := crypto.EncryptRec1(token, sid, timeSecs, timeMillis)

	var steamPatcher uint32
	if opts.SteamPatcher {
		steamPatcher = 1
	}
	err = login.NewClient(client).UserAuthenV3(&login.UserAuthenV3{
		Rec1:           string(rec1),
		Locale:         opts.Locale,
		IsSteamPatcher: steamPatcher,
	})
	if err != nil {
		return nil, fmt.Errorf("sending auth request: %w", err)
	}

	select {
	case rsp := <-handler.ch:
		if rsp.Error != 0 {
			return nil, fmt.Errorf("login failed (code %d): %s", rsp.Error, rsp.Reason)
		}
		ck2 := crypto.DecryptRec1([]byte(rsp.Rec1), sid, timeSecs, timeMillis)
		return &Session{UserID: rsp.UserID, CK2: string(ck2)}, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for auth response: %w", ctx.Err())
	}
}

// responseTimeout bounds the wait for the login server's auth reply once
// connected.
const responseTimeout = 15 * time.Second

// dialWithRetries gives each connection attempt its own timeout and backs off
// briefly between attempts, so a dropped connection is retried instead of
// burning through every attempt in a tight loop.
func dialWithRetries(ctx context.Context, server string, router *proto.MessageRouter, retries int, timeout time.Duration) (*proto.Client, error) {
	var err error
	for attempt := range retries {
		if attempt > 0 {
			select {
			case <-time.After(time.Duration(attempt) * 250 * time.Millisecond):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		dialCtx, cancel := context.WithTimeout(ctx, timeout)
		var client *proto.Client
		client, err = proto.Dial(dialCtx, server, router)
		cancel()
		if err == nil {
			return client, nil
		}
	}
	return nil, err
}
