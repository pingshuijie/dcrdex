// This code is available on the terms of the project LICENSE.md file,
// also available online at https://blueoakcouncil.org/license/1.0.0.

package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"decred.org/dcrdex/dex"
	"decred.org/dcrdex/dex/encode"
	"decred.org/dcrdex/dex/msgjson"
	"decred.org/dcrdex/dex/order"
	ordertest "decred.org/dcrdex/dex/order/test"
	"decred.org/dcrdex/server/account"
	"decred.org/dcrdex/server/comms"
	"decred.org/dcrdex/server/db"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
)

func noop() {}

func randBytes(l int) []byte {
	b := make([]byte, l)
	rand.Read(b)
	return b
}

var randomMatchID = ordertest.RandomMatchID

type ratioData struct {
	oidsCompleted  []order.OrderID
	timesCompleted []int64
	oidsCancels    []order.OrderID
	oidsCanceled   []order.OrderID
	timesCanceled  []int64
	epochGaps      []int32
}

// TStorage satisfies the Storage interface
type TStorage struct {
	acctInfo            *db.Account
	acctInfoErr         error
	acct                *account.Account
	matches             []*db.MatchData
	matchStatuses       []*db.MatchStatus
	userPreimageResults []*db.PreimageResult
	userMatchOutcomes   []*db.MatchOutcome
	orderStatuses       []*db.OrderStatus
	acctErr             error
	regAddr             string
	regAsset            uint32
	bonds               []*db.Bond
	ratio               ratioData
}

func (s *TStorage) AccountInfo(account.AccountID) (*db.Account, error) {
	return s.acctInfo, s.acctInfoErr
}
func (s *TStorage) Account(acct account.AccountID, lockTimeThresh time.Time) (*account.Account, []*db.Bond) {
	return s.acct, s.bonds
}
func (s *TStorage) setBondTier(tier uint32) {
	s.bonds = []*db.Bond{{Strength: tier, LockTime: time.Now().Unix() * 2}}
}
func (s *TStorage) CreateAccountWithBond(acct *account.Account, bond *db.Bond) error { return nil }
func (s *TStorage) AddBond(acct account.AccountID, bond *db.Bond) error              { return nil }
func (s *TStorage) DeleteBond(assetID uint32, coinID []byte) error                   { return nil }
func (s *TStorage) FetchPrepaidBond([]byte) (uint32, int64, error) {
	return 1, time.Now().Add(time.Hour * 48).Unix(), nil
}
func (s *TStorage) DeletePrepaidBond(coinID []byte) (err error) { return nil }
func (s *TStorage) StorePrepaidBonds(coinIDs [][]byte, strength uint32, lockTime int64) error {
	return nil
}
func (s *TStorage) CompletedAndAtFaultMatchStats(aid account.AccountID, lastN int) ([]*db.MatchOutcome, error) {
	return s.userMatchOutcomes, nil
}
func (s *TStorage) UserMatchFails(aid account.AccountID, lastN int) ([]*db.MatchFail, error) {
	return nil, nil
}
func (s *TStorage) PreimageStats(user account.AccountID, lastN int) ([]*db.PreimageResult, error) {
	return s.userPreimageResults, nil
}
func (s *TStorage) ForgiveMatchFail(mid order.MatchID) (bool, error) {
	return false, nil
}
func (s *TStorage) UserOrderStatuses(aid account.AccountID, base, quote uint32, oids []order.OrderID) ([]*db.OrderStatus, error) {
	return s.orderStatuses, nil
}
func (s *TStorage) ActiveUserOrderStatuses(aid account.AccountID) ([]*db.OrderStatus, error) {
	var activeOrderStatuses []*db.OrderStatus
	for _, orderStatus := range s.orderStatuses {
		if orderStatus.Status == order.OrderStatusEpoch || orderStatus.Status == order.OrderStatusBooked {
			activeOrderStatuses = append(activeOrderStatuses, orderStatus)
		}
	}
	return activeOrderStatuses, nil
}
func (s *TStorage) AllActiveUserMatches(account.AccountID) ([]*db.MatchData, error) {
	return s.matches, nil
}
func (s *TStorage) MatchStatuses(aid account.AccountID, base, quote uint32, matchIDs []order.MatchID) ([]*db.MatchStatus, error) {
	return s.matchStatuses, nil
}
func (s *TStorage) CreateAccount(acct *account.Account, assetID uint32, addr string) error {
	s.regAddr = addr
	s.regAsset = assetID
	return s.acctErr
}
func (s *TStorage) setRatioData(dat *ratioData) {
	s.ratio = *dat
}
func (s *TStorage) CompletedUserOrders(aid account.AccountID, _ int) (oids []order.OrderID, compTimes []int64, err error) {
	return s.ratio.oidsCompleted, s.ratio.timesCompleted, nil
}
func (s *TStorage) ExecutedCancelsForUser(aid account.AccountID, _ int) (cancels []*db.CancelRecord, err error) {
	for i := range s.ratio.oidsCanceled {
		cancels = append(cancels, &db.CancelRecord{
			ID:        s.ratio.oidsCancels[i],
			TargetID:  s.ratio.oidsCanceled[i],
			MatchTime: s.ratio.timesCanceled[i],
			EpochGap:  s.ratio.epochGaps[i],
		})
	}
	return cancels, nil
}

func (s *TStorage) GetUserReputationData(ctx context.Context, user account.AccountID, pimgSz, matchSz, orderSz int) ([]*db.PreimageOutcome, []*db.MatchResult, []*db.OrderOutcome, error) {
	return nil, nil, nil, nil
}

func (s *TStorage) AddPreimageOutcome(ctx context.Context, user account.AccountID, oid order.OrderID, miss bool) (*db.PreimageOutcome, error) {
	return nil, nil
}

func (s *TStorage) AddMatchOutcome(ctx context.Context, user account.AccountID, mid order.MatchID, outcome Outcome) (*db.MatchResult, error) {
	return nil, nil
}

var dbIDCounter int64

func nextDBID() int64 {
	return atomic.AddInt64(&dbIDCounter, 1)
}

func (s *TStorage) AddOrderOutcome(ctx context.Context, user account.AccountID, oid order.OrderID, canceled bool) (*db.OrderOutcome, error) {
	return &db.OrderOutcome{DBID: nextDBID(), OrderID: oid, Canceled: canceled}, nil
}

func (s *TStorage) PruneOutcomes(ctx context.Context, user account.AccountID, outcomeClass db.OutcomeClass, fromDBID int64) error {
	return nil
}

func (s *TStorage) GetUserReputationVersion(ctx context.Context, user account.AccountID) (int16, error) {
	return 0, nil
}

func (s *TStorage) UpgradeUserReputationV1(
	ctx context.Context, user account.AccountID, pimgs []*db.PreimageOutcome, matches []*db.MatchResult, ords []*db.OrderOutcome, /* Without DB IDs */
) ([]*db.PreimageOutcome, []*db.MatchResult, []*db.OrderOutcome, error) /* With DB IDs */ {
	return pimgs, matches, ords, nil
}

func (s *TStorage) ForgiveUser(ctx context.Context, user account.AccountID) error {
	return nil
}

// TSigner satisfies the Signer interface
type TSigner struct {
	sig *ecdsa.Signature
	//privKey *secp256k1.PrivateKey
	pubkey *secp256k1.PublicKey
}

// Maybe actually change this to an ecdsa.Sign with a private key instead?
func (s *TSigner) Sign(hash []byte) *ecdsa.Signature { return s.sig }
func (s *TSigner) PubKey() *secp256k1.PublicKey      { return s.pubkey }

type tReq struct {
	msg      *msgjson.Message
	respFunc func(comms.Link, *msgjson.Message)
}

// tRPCClient satisfies the comms.Link interface.
type TRPCClient struct {
	id         uint64
	ip         dex.IPKey
	addr       string
	sendErr    error
	sendRawErr error
	requestErr error
	banished   bool
	sends      []*msgjson.Message
	reqs       []*tReq
	on         uint32
	closed     chan struct{}
}

func (c *TRPCClient) ID() uint64    { return c.id }
func (c *TRPCClient) IP() dex.IPKey { return c.ip }
func (c *TRPCClient) Addr() string  { return c.addr }
func (c *TRPCClient) Authorized()   {}
func (c *TRPCClient) Send(msg *msgjson.Message) error {
	c.sends = append(c.sends, msg)
	return c.sendErr
}
func (c *TRPCClient) SendRaw(b []byte) error {
	if c.sendRawErr != nil {
		return c.sendRawErr
	}
	msg, err := msgjson.DecodeMessage(b)
	if err != nil {
		return err
	}
	c.sends = append(c.sends, msg)
	return nil
}
func (c *TRPCClient) SendError(id uint64, msg *msgjson.Error) {
}
func (c *TRPCClient) Request(msg *msgjson.Message, f func(comms.Link, *msgjson.Message), _ time.Duration, _ func()) error {
	c.reqs = append(c.reqs, &tReq{
		msg:      msg,
		respFunc: f,
	})
	return c.requestErr
}
func (c *TRPCClient) RequestRaw(msgID uint64, rawMsg []byte, f func(comms.Link, *msgjson.Message), expireTime time.Duration, expire func()) error {
	return nil
}

func (c *TRPCClient) Done() <-chan struct{} {
	return c.closed
}
func (c *TRPCClient) Disconnect() {
	if atomic.CompareAndSwapUint32(&c.on, 0, 1) {
		close(c.closed)
	}
}
func (c *TRPCClient) Banish() { c.banished = true }
func (c *TRPCClient) getReq() *tReq {
	if len(c.reqs) == 0 {
		return nil
	}
	req := c.reqs[0]
	c.reqs = c.reqs[1:]
	return req
}
func (c *TRPCClient) getSend() *msgjson.Message {
	if len(c.sends) == 0 {
		return nil
	}
	msg := c.sends[0]
	c.sends = c.sends[1:]
	return msg
}

func (c *TRPCClient) CustomID() string {
	return ""
}

func (c *TRPCClient) SetCustomID(string) {}

var tClientID uint64

func tNewRPCClient() *TRPCClient {
	tClientID++
	return &TRPCClient{
		id:     tClientID,
		ip:     dex.NewIPKey("123.123.123.123"),
		addr:   "addr",
		closed: make(chan struct{}),
	}
}

var tAcctID uint64

func newAccountID() account.AccountID {
	tAcctID++
	ib := make([]byte, 8)
	binary.BigEndian.PutUint64(ib, tAcctID)
	var acctID account.AccountID
	copy(acctID[len(acctID)-8:], ib)
	return acctID
}

type tUser struct {
	conn    *TRPCClient
	acctID  account.AccountID
	privKey *secp256k1.PrivateKey
}

// makes a new user with its own account ID, tRPCClient
func tNewUser(t *testing.T) *tUser {
	t.Helper()
	conn := tNewRPCClient()
	privKey, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("error generating private key: %v", err)
	}
	acctID := account.NewID(privKey.PubKey().SerializeCompressed())
	return &tUser{
		conn:    conn,
		acctID:  acctID,
		privKey: privKey,
	}
}

func (u *tUser) randomSignature() *ecdsa.Signature {
	return ecdsa.Sign(u.privKey, randBytes(32))
}

type testRig struct {
	mgr     *AuthManager
	storage *TStorage
	signer  *TSigner
}

var rig *testRig

type tSignable struct {
	b   []byte
	sig []byte
}

func (s *tSignable) SetSig(b []byte)  { s.sig = b }
func (s *tSignable) SigBytes() []byte { return s.sig }
func (s *tSignable) Serialize() []byte {
	return s.b
}

func signMsg(priv *secp256k1.PrivateKey, msg []byte) []byte {
	hash := sha256.Sum256(msg)
	sig := ecdsa.Sign(priv, hash[:])
	return sig.Serialize()
}

func tNewConnect(user *tUser) *msgjson.Connect {
	return &msgjson.Connect{
		AccountID:  user.acctID[:],
		APIVersion: 0,
		Time:       uint64(time.Now().UnixMilli()),
	}
}

func extractConnectResult(t *testing.T, msg *msgjson.Message) *msgjson.ConnectResult {
	t.Helper()
	if msg == nil {
		t.Fatalf("no response from 'connect' request")
	}
	resp, _ := msg.Response()
	result := new(msgjson.ConnectResult)
	err := json.Unmarshal(resp.Result, result)
	if err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	return result
}

func queueUser(t *testing.T, user *tUser) *msgjson.Message {
	t.Helper()
	rig.storage.acct = &account.Account{ID: user.acctID, PubKey: user.privKey.PubKey()}
	connect := tNewConnect(user)
	sigMsg := connect.Serialize()
	sig := signMsg(user.privKey, sigMsg)
	connect.SetSig(sig)
	msg, _ := msgjson.NewRequest(comms.NextID(), msgjson.ConnectRoute, connect)
	return msg
}

func connectUser(t *testing.T, user *tUser) *msgjson.Message {
	t.Helper()
	return tryConnectUser(t, user, false)
}

func tryConnectUser(t *testing.T, user *tUser, wantErr bool) *msgjson.Message {
	t.Helper()
	connect := queueUser(t, user)
	err := rig.mgr.handleConnect(user.conn, connect)
	if (err != nil) != wantErr {
		t.Fatalf("handleConnect: wantErr=%v, got err=%v", wantErr, err)
	}

	// Check the response.
	respMsg := user.conn.getSend()
	if respMsg == nil {
		t.Fatalf("no response from 'connect' request")
	}
	if respMsg.ID != connect.ID {
		t.Fatalf("'connect' response has wrong ID. expected %d, got %d", connect.ID, respMsg.ID)
	}
	return respMsg
}

func makeEnsureErr(t *testing.T) func(rpcErr *msgjson.Error, tag string, code int) {
	return func(rpcErr *msgjson.Error, tag string, code int) {
		t.Helper()
		if rpcErr == nil {
			t.Fatalf("no error for %s ID", tag)
		}
		if rpcErr.Code != code {
			t.Fatalf("wrong error code for %s. expected %d, got %d: %s",
				tag, code, rpcErr.Code, rpcErr.Message)
		}
	}
}

func waitFor(pred func() bool, timeout time.Duration) (fail bool) {
	tStart := time.Now()
	for {
		if pred() {
			return false
		}
		if time.Since(tStart) > timeout {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
}

var (
	tBondConfs       int64 = 5
	tParseBondTxAcct account.AccountID
	tParseBondTxErr  error
)

func tParseBondTx(assetID uint32, ver uint16, rawTx []byte) (bondCoinID []byte, amt int64,
	lockTime int64, acct account.AccountID, err error) {
	return nil, 0, time.Now().Add(time.Minute).Unix(), tParseBondTxAcct, tParseBondTxErr
}

const (
	tRegFee       uint64 = 500_000_000
	tDexPubKeyHex string = "032e3678f9889206dcea4fc281556c9e543c5d5ffa7efe8d11118b52e29c773f27"
)

var tDexPubKeyBytes = []byte{
	0x03, 0x2e, 0x36, 0x78, 0xf9, 0x88, 0x92, 0x06, 0xdc, 0xea, 0x4f, 0xc2,
	0x81, 0x55, 0x6c, 0x9e, 0x54, 0x3c, 0x5d, 0x5f, 0xfa, 0x7e, 0xfe, 0x8d,
	0x11, 0x11, 0x8b, 0x52, 0xe2, 0x9c, 0x77, 0x3f, 0x27,
}

var tRoutes = make(map[string]comms.MsgHandler)

func TestMain(m *testing.M) {
	doIt := func() int {
		UseLogger(dex.StdOutLogger("AUTH_TEST", dex.LevelTrace))
		ctx, shutdown := context.WithCancel(context.Background())
		defer shutdown()
		storage := &TStorage{}
		// secp256k1.PrivKeyFromBytes
		dexKey, _ := secp256k1.ParsePubKey(tDexPubKeyBytes)
		signer := &TSigner{pubkey: dexKey}
		authMgr := NewAuthManager(&Config{
			Storage:    storage,
			Signer:     signer,
			BondExpiry: 86400,
			BondAssets: map[string]*msgjson.BondAsset{
				"dcr": {
					Version: 0,
					ID:      42,
					Confs:   uint32(tBondConfs),
					Amt:     tRegFee * 10,
				},
			},
			BondTxParser:    tParseBondTx,
			UserUnbooker:    func(account.AccountID) {},
			MiaUserTimeout:  90 * time.Second, // TODO: test
			CancelThreshold: 0.9,
			TxDataSources:   make(map[uint32]TxDataSource),
			Route: func(route string, handler comms.MsgHandler) {
				tRoutes[route] = handler
			},
		})
		cm := dex.NewConnectionMaster(authMgr)
		cm.Connect(ctx)
		defer cm.Disconnect()
		rig = &testRig{
			storage: storage,
			signer:  signer,
			mgr:     authMgr,
		}
		return m.Run()
	}

	os.Exit(doIt())
}

func userMatchData(takerUser account.AccountID) (*db.MatchData, *order.UserMatch) {
	var baseRate, quoteRate uint64 = 123, 73
	side := order.Taker
	takerSell := true
	feeRateSwap := baseRate // user is selling

	anyID := newAccountID()
	var mid order.MatchID
	copy(mid[:], anyID[:])
	anyID = newAccountID()
	var oid order.OrderID
	copy(oid[:], anyID[:])
	takerUserMatch := &order.UserMatch{
		OrderID:     oid,
		MatchID:     mid,
		Quantity:    1,
		Rate:        2,
		Address:     "makerSwapAddress", // counterparty
		Status:      order.MakerRedeemed,
		Side:        side,
		FeeRateSwap: feeRateSwap,
	}

	var oid2 order.OrderID
	anyID = newAccountID()
	copy(oid2[:], anyID[:])
	matchData := &db.MatchData{
		ID:        mid,
		Taker:     oid,
		TakerAcct: takerUser,
		TakerAddr: "takerSwapAddress",
		TakerSell: takerSell,
		Maker:     oid2,
		MakerAcct: newAccountID(),
		MakerAddr: takerUserMatch.Address,
		Epoch: order.EpochID{
			Dur: 10000,
			Idx: 132412342,
		},
		Quantity:  takerUserMatch.Quantity,
		Rate:      takerUserMatch.Rate,
		BaseRate:  baseRate,
		QuoteRate: quoteRate,
		Active:    true,
		Status:    takerUserMatch.Status,
	}

	//matchTime := matchData.Epoch.End()
	return matchData, takerUserMatch
}

func TestGraceLimit(t *testing.T) {
	tests := []struct {
		name      string
		thresh    float64
		wantLimit int
	}{
		{"0.99 => 99", 0.99, 99}, // 98.99999999999991
		{"0.98 => 49", 0.98, 49}, // 48.99999999999996
		{"0.96 => 24", 0.96, 24}, // 23.99999999999998
		{"0.95 => 19", 0.95, 19}, // 18.999999999999982
		{"0.9 => 9", 0.9, 9},     // 9.000000000000002
		{"0.875 => 7", 0.875, 7}, // exact
		{"0.8 => 4", 0.8, 4},     // 4.000000000000001
		{"0.75 => 3", 0.75, 3},   // exact
		{"0.5 => 1", 0.5, 1},     // exact
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := &AuthManager{
				cancelThresh: tt.thresh,
			}
			got := auth.GraceLimit()
			if got != tt.wantLimit {
				t.Errorf("incorrect grace limit. got %d, want %d", got, tt.wantLimit)
			}
		})
	}
}

var t0 = int64(1601418963000)

func nextTime() int64 {
	t0 += 10
	return t0
}

func newMatchOutcome(status order.MatchStatus, mid order.MatchID, fail bool, val uint64, t int64) *db.MatchOutcome {
	switch status {
	case order.NewlyMatched, order.MakerSwapCast, order.TakerSwapCast:
		if !fail {
			panic("wrong")
		}
	case order.MatchComplete:
		if fail {
			panic("wrong")
		}
	}
	return &db.MatchOutcome{
		Status: status,
		ID:     mid,
		Fail:   fail,
		Time:   t,
		Value:  val,
	}
}

func randomOrderID() (oid order.OrderID) {
	copy(oid[:], encode.RandomBytes(32))
	return
}

func newPreimageResult(miss bool, t int64) *db.PreimageResult {
	return &db.PreimageResult{
		Miss: miss,
		Time: t,
		ID:   randomOrderID(),
	}
}

func setViolations() (wantScore int32) {
	rig.storage.userMatchOutcomes = []*db.MatchOutcome{
		newMatchOutcome(order.NewlyMatched, randomMatchID(), true, 7, nextTime()),
		newMatchOutcome(order.MatchComplete, randomMatchID(), false, 7, nextTime()), // success
		newMatchOutcome(order.NewlyMatched, randomMatchID(), true, 7, nextTime()),
		newMatchOutcome(order.MakerSwapCast, randomMatchID(), true, 7, nextTime()), // noSwapAsTaker at index 3
		newMatchOutcome(order.TakerSwapCast, randomMatchID(), true, 7, nextTime()),
		newMatchOutcome(order.MakerRedeemed, randomMatchID(), false, 7, nextTime()), // success (for maker)
		newMatchOutcome(order.MakerRedeemed, randomMatchID(), true, 7, nextTime()),
		newMatchOutcome(order.MatchComplete, randomMatchID(), false, 7, nextTime()), // success
		newMatchOutcome(order.MatchComplete, randomMatchID(), false, 7, nextTime()), // success
	}
	t0 -= 4000
	rig.storage.userPreimageResults = []*db.PreimageResult{newPreimageResult(true, nextTime())}
	for range rig.storage.userMatchOutcomes {
		rig.storage.userPreimageResults = append(rig.storage.userPreimageResults, newPreimageResult(false, nextTime()))
	}
	return 4*matchCompletedScore + 1*preimageMissScore +
		2*noSwapAsMakerScore + noSwapAsTakerScore + noRedeemAsMakerScore + 1*noRedeemAsTakerScore
}

func clearViolations() {
	rig.storage.userMatchOutcomes = []*db.MatchOutcome{}
}

func TestAuthManager_loadUserScore(t *testing.T) {
	// Spot test with all violations set
	wantScore := setViolations()
	defer clearViolations()
	user := tNewUser(t)
	score, err := rig.mgr.loadUserScore(user.acctID)
	if err != nil {
		t.Fatal(err)
	}
	if score != wantScore {
		t.Errorf("wrong score. got %d, want %d", score, wantScore)
	}

	// add one NoSwapAsTaker (match inactive at MakerSwapCast)
	rig.storage.userMatchOutcomes = append(rig.storage.userMatchOutcomes,
		newMatchOutcome(order.MakerSwapCast, randomMatchID(), true, 7, nextTime()))
	wantScore += noSwapAsTakerScore

	score, err = rig.mgr.loadUserScore(user.acctID)
	if err != nil {
		t.Fatal(err)
	}
	if score != wantScore {
		t.Errorf("wrong score. got %d, want %d", score, wantScore)
	}

	tests := []struct {
		name           string
		user           account.AccountID
		matchOutcomes  []*db.MatchOutcome
		preimageMisses []*db.PreimageResult
		wantScore      int32
	}{
		{
			name: "negative",
			user: user.acctID,
			matchOutcomes: []*db.MatchOutcome{
				newMatchOutcome(order.MatchComplete, randomMatchID(), false, 7, nextTime()),
				newMatchOutcome(order.MatchComplete, randomMatchID(), false, 7, nextTime()),
				newMatchOutcome(order.MatchComplete, randomMatchID(), false, 7, nextTime()),
				newMatchOutcome(order.MatchComplete, randomMatchID(), false, 7, nextTime()),
			},
			wantScore: 4,
		},
		{
			name:          "nuthin",
			user:          user.acctID,
			matchOutcomes: []*db.MatchOutcome{},
			wantScore:     0,
		},
		{
			name: "balance",
			user: user.acctID,
			matchOutcomes: []*db.MatchOutcome{
				newMatchOutcome(order.MatchComplete, randomMatchID(), false, 7, nextTime()),
				newMatchOutcome(order.MatchComplete, randomMatchID(), false, 7, nextTime()),
				newMatchOutcome(order.MatchComplete, randomMatchID(), false, 7, nextTime()),
				newMatchOutcome(order.MatchComplete, randomMatchID(), false, 7, nextTime()),
			},
			preimageMisses: []*db.PreimageResult{
				newPreimageResult(true, nextTime()),
				newPreimageResult(true, nextTime()),
			},
			wantScore: 0,
		},
		{
			name: "tipping red",
			user: user.acctID,
			matchOutcomes: []*db.MatchOutcome{
				newMatchOutcome(order.NewlyMatched, randomMatchID(), true, 7, nextTime()),
				newMatchOutcome(order.MakerSwapCast, randomMatchID(), true, 7, nextTime()),
				newMatchOutcome(order.MatchComplete, randomMatchID(), false, 7, nextTime()),
				newMatchOutcome(order.MatchComplete, randomMatchID(), false, 7, nextTime()),
				newMatchOutcome(order.MatchComplete, randomMatchID(), false, 7, nextTime()),
				newMatchOutcome(order.NewlyMatched, randomMatchID(), true, 7, nextTime()),
				newMatchOutcome(order.MakerRedeemed, randomMatchID(), true, 7, nextTime()),
				newMatchOutcome(order.MatchComplete, randomMatchID(), false, 7, nextTime()),
				newMatchOutcome(order.MatchComplete, randomMatchID(), false, 7, nextTime()),
			},
			preimageMisses: []*db.PreimageResult{
				newPreimageResult(true, nextTime()),
				newPreimageResult(false, nextTime()),
			},
			wantScore: 2*noSwapAsMakerScore + 1*noSwapAsTakerScore + 0*noRedeemAsMakerScore +
				1*noRedeemAsTakerScore + 1*preimageMissScore + 5*matchCompletedScore,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rig.storage.userMatchOutcomes = tt.matchOutcomes
			rig.storage.userPreimageResults = tt.preimageMisses
			score, err := rig.mgr.loadUserScore(tt.user)
			if err != nil {
				t.Fatalf("got err: %v", err)
			}
			if score != tt.wantScore {
				t.Errorf("incorrect user score. got %d, want %d", score, tt.wantScore)
			}
		})
	}
}

func TestConnect(t *testing.T) {
	user := tNewUser(t)
	rig.signer.sig = user.randomSignature()

	// Before connecting, put an activeOrder and activeMatch in storage.
	matchData, userMatch := userMatchData(user.acctID)
	matchTime := matchData.Epoch.End()

	rig.storage.orderStatuses = []*db.OrderStatus{
		{
			ID:     userMatch.OrderID,
			Status: order.OrderStatusBooked,
		},
	}
	defer func() { rig.storage.orderStatuses = nil }()

	rig.storage.matches = []*db.MatchData{matchData}
	defer func() { rig.storage.matches = nil }()

	epochGaps := []int32{1} // penalized

	rig.storage.setRatioData(&ratioData{
		oidsCompleted:  []order.OrderID{{0x1}},
		timesCompleted: []int64{1234},
		oidsCancels:    []order.OrderID{{0x2}},
		oidsCanceled:   []order.OrderID{{0x1}},
		timesCanceled:  []int64{1235},
		epochGaps:      epochGaps,
	}) // 1:1 = 50%
	defer rig.storage.setRatioData(&ratioData{}) // clean slate

	// TODO: update tests now that there is are no close/ban and unban
	// operations, instead an integral tier.

	// TODO: update tests now that cancel ratio is part of the score equation
	// rather than a hard close operation.

	/* cancel ratio stuff

	// Close account on connect with failing cancel ratio, and no grace period.
	rig.mgr.cancelThresh = 0.2 // thresh below actual ratio, and no grace period with total/(1+total) = 2/3 = 0.66... > 0.2
	tryConnectUser(t, user, false)
	if rig.storage.closedID != user.acctID {
		t.Fatalf("Expected account %v to be closed on connect, got %v", user.acctID, rig.storage.closedID)
	}

	// Make it a free cancel.
	rig.storage.closedID = account.AccountID{} // unclose the account in db
	epochGaps[0] = 2
	connectUser(t, user)
	if rig.storage.closedID == user.acctID {
		t.Fatalf("Expected account %v to NOT be closed with free cancels, but it was.", user)
	}
	epochGaps[0] = 1

	// Try again just meeting cancel ratio.
	rig.storage.closedID = account.AccountID{} // unclose the account in db
	rig.mgr.cancelThresh = 0.6                 // passable threshold for 1 cancel : 1 completion (0.5)

	connectUser(t, user)
	if rig.storage.closedID == user.acctID {
		t.Fatalf("Expected account %v to NOT be closed on connect, but it was.", user)
	}

	// Add another cancel, bringing cancels to 2, completions 1 for a ratio of
	// 2:1 (2/3 = 0.666...), and total/(1+total) = 3/4 = 0.75 > thresh (0.6), so
	// no grace period.
	rig.storage.ratio.oidsCanceled = append(rig.storage.ratio.oidsCanceled, order.OrderID{0x3})
	rig.storage.ratio.oidsCancels = append(rig.storage.ratio.oidsCancels, order.OrderID{0x4})
	rig.storage.ratio.timesCanceled = append(rig.storage.ratio.timesCanceled, 12341234)
	rig.storage.ratio.epochGaps = append(rig.storage.ratio.epochGaps, 1)

	tryConnectUser(t, user, false)
	if rig.storage.closedID != user.acctID {
		t.Fatalf("Expected account %v to be closed on connect, got %v", user.acctID, rig.storage.closedID)
	}

	// Make one a free cancel.
	rig.storage.closedID = account.AccountID{} // unclose the account in db
	rig.storage.ratio.epochGaps[1] = 2
	connectUser(t, user)
	if rig.storage.closedID == user.acctID {
		t.Fatalf("Expected account %v to NOT be closed with free cancels, but it was.", user)
	}
	rig.storage.ratio.epochGaps[1] = 0

	// Try again just meeting cancel ratio.
	rig.storage.closedID = account.AccountID{} // unclose the account in db
	rig.mgr.cancelThresh = 0.7                 // passable threshold for 2 cancel : 1 completion (0.6666..)

	tryConnectUser(t, user, false)
	if rig.storage.closedID == user.acctID {
		t.Fatalf("Expected account %v to NOT be closed on connect, but it was.", user)
	}

	// Test the grace period (threshold <= total/(1+total) and no completions)
	// 2 cancels, 0 success, 2 total
	rig.mgr.cancelThresh = 0.7             // 2/(1+2) = 0.66.. < threshold
	rig.storage.ratio.timesCompleted = nil // no completions
	rig.storage.ratio.oidsCompleted = nil
	tryConnectUser(t, user, false)
	if rig.storage.closedID == user.acctID {
		t.Fatalf("Expected account %v to NOT be closed on connect, but it was.", user)
	}

	// 3 cancels, 0 success, 3 total => rate = 1.0, exceeds threshold
	rig.mgr.cancelThresh = 0.75 // 3/(1+3) == threshold, still in grace period
	rig.storage.ratio.oidsCanceled = append(rig.storage.ratio.oidsCanceled, order.OrderID{0x4})
	rig.storage.ratio.oidsCancels = append(rig.storage.ratio.oidsCancels, order.OrderID{0x5})
	rig.storage.ratio.timesCanceled = append(rig.storage.ratio.timesCanceled, 12341239)
	rig.storage.ratio.epochGaps = append(rig.storage.ratio.epochGaps, 1)

	tryConnectUser(t, user, false)
	if rig.storage.closedID == user.acctID {
		t.Fatalf("Expected account %v to NOT be closed on connect, but it was.", user)
	}

	*/

	// Connect with a violation score above revocation threshold.
	wantScore := setViolations()
	defer clearViolations()

	if wantScore > rig.mgr.penaltyThreshold {
		t.Fatalf("test score of %v is not at least the revocation threshold of %v, revise the test", wantScore, rig.mgr.penaltyThreshold)
	}

	// Test loadUserScore while here.
	_, err := rig.mgr.loadUserScore(user.acctID)
	if err != nil {
		t.Fatal(err)
	}

	// if score != wantScore {
	// 	t.Errorf("wrong score. got %d, want %d", score, wantScore)
	// }

	// No error, but Penalize account that was not previously closed.
	tryConnectUser(t, user, false)

	makerSwapCastIdx := 3
	rig.storage.userMatchOutcomes = append(rig.storage.userMatchOutcomes[:makerSwapCastIdx], rig.storage.userMatchOutcomes[makerSwapCastIdx+1:]...)
	wantScore -= noSwapAsTakerScore
	if wantScore <= rig.mgr.penaltyThreshold {
		t.Fatalf("test score of %v is not more than the penalty threshold of %v, revise the test", wantScore, rig.mgr.penaltyThreshold)
	}
	_, err = rig.mgr.loadUserScore(user.acctID)
	if err != nil {
		t.Fatal(err)
	}
	// if score != wantScore {
	// 	t.Errorf("wrong score. got %d, want %d", score, wantScore)
	// }

	// Connect the user.
	respMsg := connectUser(t, user)
	cResp := extractConnectResult(t, respMsg)
	if len(cResp.ActiveOrderStatuses) != 1 {
		t.Fatalf("no active orders")
	}
	msgOrder := cResp.ActiveOrderStatuses[0]
	if msgOrder.ID.String() != userMatch.OrderID.String() {
		t.Fatal("active order ID mismatch: ", msgOrder.ID.String(), " != ", userMatch.OrderID.String())
	}
	if msgOrder.Status != uint16(order.OrderStatusBooked) {
		t.Fatal("active order Status mismatch: ", msgOrder.Status, " != ", order.OrderStatusBooked)
	}
	if len(cResp.ActiveMatches) != 1 {
		t.Fatalf("no active matches")
	}
	msgMatch := cResp.ActiveMatches[0]
	if msgMatch.OrderID.String() != userMatch.OrderID.String() {
		t.Fatal("active match OrderID mismatch: ", msgMatch.OrderID.String(), " != ", userMatch.OrderID.String())
	}
	if msgMatch.MatchID.String() != userMatch.MatchID.String() {
		t.Fatal("active match MatchID mismatch: ", msgMatch.MatchID.String(), " != ", userMatch.MatchID.String())
	}
	if msgMatch.Quantity != userMatch.Quantity {
		t.Fatal("active match Quantity mismatch: ", msgMatch.Quantity, " != ", userMatch.Quantity)
	}
	if msgMatch.Rate != userMatch.Rate {
		t.Fatal("active match Rate mismatch: ", msgMatch.Rate, " != ", userMatch.Rate)
	}
	if msgMatch.Address != userMatch.Address {
		t.Fatal("active match Address mismatch: ", msgMatch.Address, " != ", userMatch.Address)
	}
	if msgMatch.Status != uint8(userMatch.Status) {
		t.Fatal("active match Status mismatch: ", msgMatch.Status, " != ", userMatch.Status)
	}
	if msgMatch.Side != uint8(userMatch.Side) {
		t.Fatal("active match Side mismatch: ", msgMatch.Side, " != ", userMatch.Side)
	}
	if msgMatch.FeeRateQuote != matchData.QuoteRate {
		t.Fatal("active match quote fee rate mismatch: ", msgMatch.FeeRateQuote, " != ", matchData.QuoteRate)
	}
	if msgMatch.FeeRateBase != matchData.BaseRate {
		t.Fatal("active match base fee rate mismatch: ", msgMatch.FeeRateBase, " != ", matchData.BaseRate)
	}
	if msgMatch.ServerTime != uint64(matchTime.UnixMilli()) {
		t.Fatal("active match time mismatch: ", msgMatch.ServerTime, " != ", uint64(matchTime.UnixMilli()))
	}

	// Send a request to the client.
	type tPayload struct {
		A int
	}
	a5 := &tPayload{A: 5}
	reqID := comms.NextID()
	msg, err := msgjson.NewRequest(reqID, "request", a5)
	if err != nil {
		t.Fatalf("NewRequest error: %v", err)
	}
	var responded bool
	rig.mgr.Request(user.acctID, msg, func(comms.Link, *msgjson.Message) {
		responded = true
	})
	req := user.conn.getReq()
	if req == nil {
		t.Fatalf("no request")
	}
	var a tPayload
	err = json.Unmarshal(req.msg.Payload, &a)
	if err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if a.A != 5 {
		t.Fatalf("wrong value for A. expected 5, got %d", a.A)
	}
	// Respond to the DEX's request.
	msg = &msgjson.Message{ID: reqID}
	req.respFunc(user.conn, msg)
	if !responded {
		t.Fatalf("responded flag not set")
	}

	reuser := &tUser{
		acctID:  user.acctID,
		privKey: user.privKey,
		conn:    tNewRPCClient(),
	}
	connectUser(t, reuser)
	a10 := &tPayload{A: 10}
	msg, _ = msgjson.NewRequest(comms.NextID(), "request", a10)
	err = rig.mgr.RequestWithTimeout(reuser.acctID, msg, func(comms.Link, *msgjson.Message) {}, time.Minute, func() {})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	// The a10 message should be in the new connection
	if user.conn.getReq() != nil {
		t.Fatalf("old connection received a request after reconnection")
	}
	if reuser.conn.getReq() == nil {
		t.Fatalf("new connection did not receive the request")
	}
}

func TestAccountErrors(t *testing.T) {
	user := tNewUser(t)
	rig.signer.sig = user.randomSignature()
	connect := queueUser(t, user)

	// Put a match in storage
	matchData, userMatch := userMatchData(user.acctID)
	matchTime := matchData.Epoch.End()
	rig.storage.matches = []*db.MatchData{matchData}

	rig.mgr.handleConnect(user.conn, connect)
	rig.storage.matches = nil

	// Check the response.
	respMsg := user.conn.getSend()
	result := extractConnectResult(t, respMsg)
	if len(result.ActiveMatches) != 1 {
		t.Fatalf("expected 1 match, received %d", len(result.ActiveMatches))
	}
	match := result.ActiveMatches[0]
	if match.OrderID.String() != userMatch.OrderID.String() {
		t.Fatal("wrong OrderID: ", match.OrderID, " != ", userMatch.OrderID)
	}
	if match.MatchID.String() != userMatch.MatchID.String() {
		t.Fatal("wrong MatchID: ", match.MatchID, " != ", userMatch.OrderID)
	}
	if match.Quantity != userMatch.Quantity {
		t.Fatal("wrong Quantity: ", match.Quantity, " != ", userMatch.OrderID)
	}
	if match.Rate != userMatch.Rate {
		t.Fatal("wrong Rate: ", match.Rate, " != ", userMatch.OrderID)
	}
	if match.Address != userMatch.Address {
		t.Fatal("wrong Address: ", match.Address, " != ", userMatch.OrderID)
	}
	if match.Status != uint8(userMatch.Status) {
		t.Fatal("wrong Status: ", match.Status, " != ", userMatch.OrderID)
	}
	if match.Side != uint8(userMatch.Side) {
		t.Fatal("wrong Side: ", match.Side, " != ", userMatch.OrderID)
	}
	if match.FeeRateQuote != matchData.QuoteRate {
		t.Fatal("wrong quote fee rate: ", match.FeeRateQuote, " != ", matchData.QuoteRate)
	}
	if match.FeeRateBase != matchData.BaseRate {
		t.Fatal("wrong base fee rate: ", match.FeeRateBase, " != ", matchData.BaseRate)
	}
	if match.ServerTime != uint64(matchTime.UnixMilli()) {
		t.Fatal("wrong match time: ", match.ServerTime, " != ", uint64(matchTime.UnixMilli()))
	}
	// Make a violation score above penalty threshold reflected by the DB.
	score := setViolations()
	defer clearViolations()

	rig.mgr.removeClient(rig.mgr.user(user.acctID)) // disconnect first, NOTE that link.Disconnect is async
	user.conn = tNewRPCClient()                     // disconnect necessitates new conn ID
	rpcErr := rig.mgr.handleConnect(user.conn, connect)
	if rpcErr != nil {
		t.Fatalf("should be no error for closed account")
	}
	client := rig.mgr.user(user.acctID)
	rig.storage.setBondTier(1)
	if client == nil {
		t.Fatalf("client not found")
	}
	initPenaltyThresh := rig.mgr.penaltyThreshold
	defer func() { rig.mgr.penaltyThreshold = initPenaltyThresh }()
	rig.mgr.penaltyThreshold = score
	if client.tier > 0 {
		t.Errorf("client should have been tier 0")
	}

	// Raise the penalty threshold to ensure automatic reinstatement.
	rig.mgr.penaltyThreshold = score - 1

	rig.mgr.removeClient(rig.mgr.user(user.acctID)) // disconnect first, NOTE that link.Disconnect is async
	user.conn = tNewRPCClient()                     // disconnect necessitates new conn ID
	rpcErr = rig.mgr.handleConnect(user.conn, connect)
	if rpcErr != nil {
		t.Fatalf("should be no error for closed account")
	}
	client = rig.mgr.user(user.acctID)
	if client == nil {
		t.Fatalf("client not found")
	}
	if client.tier < 1 {
		t.Errorf("client should have unbanned automatically")
	}

}

func TestRoute(t *testing.T) {
	user := tNewUser(t)
	rig.signer.sig = user.randomSignature()
	connectUser(t, user)

	var translated account.AccountID
	rig.mgr.Route("testroute", func(id account.AccountID, msg *msgjson.Message) *msgjson.Error {
		translated = id
		return nil
	})
	f := tRoutes["testroute"]
	if f == nil {
		t.Fatalf("'testroute' not registered")
	}
	rpcErr := f(user.conn, nil)
	if rpcErr != nil {
		t.Fatalf("rpc error: %s", rpcErr.Message)
	}
	if translated != user.acctID {
		t.Fatalf("account ID not set")
	}

	// Run the route with an unknown client. Should be an UnauthorizedConnection
	// error.
	foreigner := tNewUser(t)
	rpcErr = f(foreigner.conn, nil)
	if rpcErr == nil {
		t.Fatalf("no error for unauthed user")
	}
	if rpcErr.Code != msgjson.UnauthorizedConnection {
		t.Fatalf("wrong error for unauthed user. expected %d, got %d",
			msgjson.UnauthorizedConnection, rpcErr.Code)
	}
}

func TestAuth(t *testing.T) {
	user := tNewUser(t)
	rig.signer.sig = user.randomSignature()
	connectUser(t, user)

	msgBytes := randBytes(50)
	sigBytes := signMsg(user.privKey, msgBytes)
	err := rig.mgr.Auth(user.acctID, msgBytes, sigBytes)
	if err != nil {
		t.Fatalf("unexpected auth error: %v", err)
	}

	foreigner := tNewUser(t)
	sigBytes = signMsg(user.privKey, msgBytes)
	err = rig.mgr.Auth(foreigner.acctID, msgBytes, sigBytes)
	if err == nil {
		t.Fatalf("no auth error for foreigner")
	}

	msgBytes = randBytes(50)
	err = rig.mgr.Auth(user.acctID, msgBytes, sigBytes)
	if err == nil {
		t.Fatalf("no error for wrong message")
	}
}

func TestSign(t *testing.T) {
	sig1 := tNewUser(t).randomSignature()
	sig1Bytes := sig1.Serialize()
	rig.signer.sig = sig1
	s := &tSignable{b: randBytes(25)}
	rig.mgr.Sign(s)
	if !bytes.Equal(sig1Bytes, s.SigBytes()) {
		t.Fatalf("incorrect signature. expected %x, got %x", sig1.Serialize(), s.SigBytes())
	}

	// Try two at a time
	s2 := &tSignable{b: randBytes(25)}
	rig.mgr.Sign(s, s2)
}

func TestSend(t *testing.T) {
	user := tNewUser(t)
	rig.signer.sig = user.randomSignature()
	connectUser(t, user)
	foreigner := tNewUser(t)

	type tA struct {
		A int
	}
	payload := &tA{A: 5}
	resp, _ := msgjson.NewResponse(comms.NextID(), payload, nil)
	payload = &tA{A: 10}
	req, _ := msgjson.NewRequest(comms.NextID(), "testroute", payload)

	// Send a message to a foreigner
	rig.mgr.Send(foreigner.acctID, resp)
	if foreigner.conn.getSend() != nil {
		t.Fatalf("message magically got through to foreigner")
	}
	if user.conn.getSend() != nil {
		t.Fatalf("foreigner message sent to authed user")
	}

	// Now send to the user
	rig.mgr.Send(user.acctID, resp)
	msg := user.conn.getSend()
	if msg == nil {
		t.Fatalf("no message for authed user")
	}
	tr := new(tA)
	r, _ := msg.Response()
	err := json.Unmarshal(r.Result, tr)
	if err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if tr.A != 5 {
		t.Fatalf("expected A = 5, got A = %d", tr.A)
	}

	// Send a request to a foreigner
	rig.mgr.Request(foreigner.acctID, req, func(comms.Link, *msgjson.Message) {})
	if foreigner.conn.getReq() != nil {
		t.Fatalf("request magically got through to foreigner")
	}
	if user.conn.getReq() != nil {
		t.Fatalf("foreigner request sent to authed user")
	}

	// Send a request to an authed user.
	rig.mgr.Request(user.acctID, req, func(comms.Link, *msgjson.Message) {})
	treq := user.conn.getReq()
	if treq == nil {
		t.Fatalf("no request for user")
	}

	tr = new(tA)
	err = json.Unmarshal(treq.msg.Payload, tr)
	if err != nil {
		t.Fatalf("request unmarshal error: %v", err)
	}
	if tr.A != 10 {
		t.Fatalf("expected A = 10, got A = %d", tr.A)
	}
}

func TestConnectErrors(t *testing.T) {
	user := tNewUser(t)
	rig.storage.acct = nil
	rig.signer.sig = user.randomSignature()

	ensureErr := makeEnsureErr(t)

	// Test an invalid json payload
	msg, err := msgjson.NewRequest(comms.NextID(), "testreq", nil)
	if err != nil {
		t.Fatalf("NewRequest error for invalid payload: %v", err)
	}
	msg.Payload = []byte(`?`)
	rpcErr := rig.mgr.handleConnect(user.conn, msg)
	ensureErr(rpcErr, "invalid payload", msgjson.RPCParseError)

	connect := tNewConnect(user)
	encodeMsg := func() {
		msg, err = msgjson.NewRequest(comms.NextID(), "testreq", connect)
		if err != nil {
			t.Fatalf("NewRequest error for bad account ID: %v", err)
		}
	}
	// connect with an invalid ID
	connect.AccountID = []byte{0x01, 0x02, 0x03, 0x04}
	encodeMsg()
	rpcErr = rig.mgr.handleConnect(user.conn, msg)
	ensureErr(rpcErr, "invalid account ID", msgjson.AuthenticationError)
	connect.AccountID = user.acctID[:]

	// user unknown to storage
	encodeMsg()
	rpcErr = rig.mgr.handleConnect(user.conn, msg)
	ensureErr(rpcErr, "account unknown to storage", msgjson.AccountNotFoundError)
	rig.storage.acct = &account.Account{ID: user.acctID, PubKey: user.privKey.PubKey()}

	// bad signature
	connect.SetSig([]byte{0x09, 0x08})
	encodeMsg()
	rpcErr = rig.mgr.handleConnect(user.conn, msg)
	ensureErr(rpcErr, "bad signature", msgjson.SignatureError)

	// A send error should not return an error, but the client should not be
	// saved to the map.
	// need to "register" the user first
	msgBytes := connect.Serialize()
	connect.SetSig(signMsg(user.privKey, msgBytes))
	encodeMsg()
	user.conn.sendErr = fmt.Errorf("test error")
	rpcErr = rig.mgr.handleConnect(user.conn, msg)
	if rpcErr != nil {
		t.Fatalf("non-nil msgjson.Error after send error: %s", rpcErr.Message)
	}
	user.conn.sendErr = nil
	if rig.mgr.user(user.acctID) != nil {
		t.Fatalf("user registered with send error")
	}
	// clear the response
	if user.conn.getSend() == nil {
		t.Fatalf("no response to clear")
	}

	// success
	rpcErr = rig.mgr.handleConnect(user.conn, msg)
	if rpcErr != nil {
		t.Fatalf("error for good connect: %s", rpcErr.Message)
	}
	// clear the response
	if user.conn.getSend() == nil {
		t.Fatalf("no response to clear")
	}
}

func TestHandleResponse(t *testing.T) {
	user := tNewUser(t)
	rig.signer.sig = user.randomSignature()
	connectUser(t, user)
	foreigner := tNewUser(t)
	unknownResponse, err := msgjson.NewResponse(comms.NextID(), 10, nil)
	if err != nil {
		t.Fatalf("error encoding unknown response: %v", err)
	}

	// test foreigner. Really just want to make sure that this returns before
	// trying to run a nil handler function, which would panic.
	rig.mgr.handleResponse(foreigner.conn, unknownResponse)

	// test for a missing handler
	rig.mgr.handleResponse(user.conn, unknownResponse)
	m := user.conn.getSend()
	if m == nil {
		t.Fatalf("no error sent for unknown response")
	}
	resp, _ := m.Response()
	if resp.Error == nil {
		t.Fatalf("error not set in response for unknown response")
	}
	if resp.Error.Code != msgjson.UnknownResponseID {
		t.Fatalf("wrong error code for unknown response. expected %d, got %d",
			msgjson.UnknownResponseID, resp.Error.Code)
	}

	// Check that expired response handlers are removed from the map.
	client := rig.mgr.user(user.acctID)
	if client == nil {
		t.Fatalf("client not found")
	}

	newID := comms.NextID()
	client.logReq(newID, func(comms.Link, *msgjson.Message) {},
		0, func() { t.Log("expired (ok)") })
	// Wait until response handler expires.
	if waitFor(func() bool {
		client.mtx.Lock()
		defer client.mtx.Unlock()
		return len(client.respHandlers) == 0
	}, 10*time.Second) {
		t.Fatalf("expected 0 response handlers, found %d", len(client.respHandlers))
	}
	client.mtx.Lock()
	if client.respHandlers[newID] != nil {
		t.Fatalf("response handler should have been expired")
	}
	client.mtx.Unlock()

	// After logging a new request, there should still be exactly one response handler
	// present. A short sleep is added to give a chance for clean-up running in a
	// separate go-routine to finish before we continue asserting on the result.
	newID = comms.NextID()
	client.logReq(newID, func(comms.Link, *msgjson.Message) {}, time.Hour, noop)
	time.Sleep(time.Millisecond)
	client.mtx.Lock()
	if len(client.respHandlers) != 1 {
		t.Fatalf("expected 1 response handler, found %d", len(client.respHandlers))
	}
	if client.respHandlers[newID] == nil {
		t.Fatalf("wrong response handler left after cleanup cycle")
	}
	client.mtx.Unlock()
}

func TestAuthManager_RecordCancel_RecordCompletedOrder(t *testing.T) {
	user := tNewUser(t)
	rig.signer.sig = user.randomSignature()
	connectUser(t, user)

	client := rig.mgr.user(user.acctID)
	if client == nil {
		t.Fatalf("client not found")
	}

	newOrderID := func() (oid order.OrderID) {
		rand.Read(oid[:])
		return
	}

	orderOutcomes := rig.mgr.orderOutcomes[user.acctID]

	oid := newOrderID()
	tCompleted := unixMsNow()
	rig.mgr.RecordCompletedOrder(user.acctID, oid, tCompleted)

	counts := func(os *latestOutcomes[*db.OrderOutcome]) (total, cancels int) {
		m := os.binViolations()
		successes, cancels := int(m[db.OutcomeOrderComplete]), int(m[db.OutcomeOrderCanceled])
		return successes + cancels, cancels
	}
	total, cancels := counts(orderOutcomes)
	if total != 1 {
		t.Errorf("got %d total orders, expected %d", total, 1)
	}
	if cancels != 0 {
		t.Errorf("got %d cancels, expected %d", cancels, 0)
	}

	checkOrd := func(ord *db.OrderOutcome, oid order.OrderID, cancel bool, timestamp int64) {
		if ord.OrderID != oid {
			t.Errorf("completed order id mismatch. got %v, expected %v",
				ord.OrderID, oid)
		}
		if ord.Canceled != cancel {
			t.Errorf("order marked as cancel=%v, expected %v", ord.Canceled, cancel)
		}
	}

	ord := orderOutcomes.outcomes[0]
	checkOrd(ord, oid, false, tCompleted.UnixMilli())

	// another
	oid = newOrderID()
	tCompleted = tCompleted.Add(time.Millisecond) // newer
	rig.mgr.RecordCompletedOrder(user.acctID, oid, tCompleted)

	total, cancels = counts(orderOutcomes)
	if total != 2 {
		t.Errorf("got %d total orders, expected %d", total, 2)
	}
	if cancels != 0 {
		t.Errorf("got %d cancels, expected %d", cancels, 0)
	}

	ord = orderOutcomes.outcomes[1]
	checkOrd(ord, oid, false, tCompleted.UnixMilli())

	// now a cancel
	coid := newOrderID()
	tCompleted = tCompleted.Add(time.Millisecond) // newer
	rig.mgr.RecordCancel(user.acctID, coid, oid, 1, tCompleted)

	total, cancels = counts(orderOutcomes)
	if total != 3 {
		t.Errorf("got %d total orders, expected %d", total, 3)
	}
	if cancels != 1 {
		t.Errorf("got %d cancels, expected %d", cancels, 1)
	}

	ord = orderOutcomes.outcomes[2]
	checkOrd(ord, coid, true, tCompleted.UnixMilli())
}

func TestMatchStatus(t *testing.T) {
	user := tNewUser(t)
	rig.signer.sig = user.randomSignature()
	connectUser(t, user)

	rig.storage.matchStatuses = []*db.MatchStatus{{
		Status:    order.MakerSwapCast,
		IsTaker:   true,
		MakerSwap: []byte{0x01},
	}}

	tTxData := encode.RandomBytes(5)
	rig.mgr.txDataSources[0] = func([]byte) ([]byte, error) {
		return tTxData, nil
	}

	reqPayload := []msgjson.MatchRequest{{MatchID: encode.RandomBytes(32)}}

	req, _ := msgjson.NewRequest(1, msgjson.MatchStatusRoute, reqPayload)

	getStatus := func() *msgjson.MatchStatusResult {
		msgErr := rig.mgr.handleMatchStatus(user.conn, req)
		if msgErr != nil {
			t.Fatalf("handleMatchStatus error: %v", msgErr)
		}

		resp := user.conn.getSend()
		if resp == nil {
			t.Fatalf("no matches sent")
		}

		statuses := []msgjson.MatchStatusResult{}
		err := resp.UnmarshalResult(&statuses)
		if err != nil {
			t.Fatalf("UnmarshalResult error: %v", err)
		}
		if len(statuses) != 1 {
			t.Fatalf("expected 1 match, got %d", len(statuses))
		}
		return &statuses[0]
	}

	// As taker in MakerSwapCast, we expect tx data.
	status := getStatus()
	if !bytes.Equal(status.MakerTxData, tTxData) {
		t.Fatalf("wrong maker tx data. exected %x, got %s", tTxData, status.MakerTxData)
	}

	// As maker, we don't expect any tx data.
	rig.storage.matchStatuses[0].IsTaker = false
	rig.storage.matchStatuses[0].IsMaker = true
	if len(getStatus().TakerTxData) != 0 {
		t.Fatalf("got tx data as maker in MakerSwapCast")
	}

	// As maker in TakerSwapCast, we do expect tx data.
	rig.storage.matchStatuses[0].Status = order.TakerSwapCast
	rig.storage.matchStatuses[0].TakerSwap = []byte{0x01}
	txData := getStatus().TakerTxData
	if !bytes.Equal(txData, tTxData) {
		t.Fatalf("wrong taker tx data. exected %x, got %s", tTxData, txData)
	}

	reqPayload[0].MatchID = []byte{}
	req, _ = msgjson.NewRequest(1, msgjson.MatchStatusRoute, reqPayload)
	msgErr := rig.mgr.handleMatchStatus(user.conn, req)
	if msgErr == nil {
		t.Fatalf("no error for bad match ID")
	}
}

func TestOrderStatus(t *testing.T) {
	user := tNewUser(t)
	rig.signer.sig = user.randomSignature()
	connectUser(t, user)

	rig.storage.orderStatuses = []*db.OrderStatus{{}}

	reqPayload := []msgjson.OrderStatusRequest{
		{
			OrderID: encode.RandomBytes(order.OrderIDSize),
		},
	}

	req, _ := msgjson.NewRequest(1, msgjson.OrderStatusRoute, reqPayload)

	msgErr := rig.mgr.handleOrderStatus(user.conn, req)
	if msgErr != nil {
		t.Fatalf("handleOrderStatus error: %v", msgErr)
	}

	resp := user.conn.getSend()
	if resp == nil {
		t.Fatalf("no orders sent")
	}

	var statuses []*msgjson.OrderStatus
	err := resp.UnmarshalResult(&statuses)
	if err != nil {
		t.Fatalf("UnmarshalResult error: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("expected 1 order, got %d", len(statuses))
	}

	reqPayload[0].OrderID = []byte{}
	req, _ = msgjson.NewRequest(1, msgjson.OrderStatusRoute, reqPayload)
	msgErr = rig.mgr.handleOrderStatus(user.conn, req)
	if msgErr == nil {
		t.Fatalf("no error for bad order ID")
	}
}

func Test_checkSigS256(t *testing.T) {
	sig := []byte{0x30, 0, 0x02, 0x01, 9, 0x2, 0x01, 10}
	ecdsa.ParseDERSignature(sig) // panic on line 132: sigStr[2] != 0x02 after trimming to sigStr[:(1+2)]

	sig = []byte{0x30, 1, 0x02, 0x01, 9, 0x2, 0x01, 10}
	ecdsa.ParseDERSignature(sig) // panic on line 139: rLen := int(sigStr[index]) with index=3 and len = 3
}
