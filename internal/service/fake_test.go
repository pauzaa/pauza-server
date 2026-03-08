package service

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/IsorilovA/pauza-server/internal/mail"
	"github.com/IsorilovA/pauza-server/internal/repository"
)

// ---------------------------------------------------------------------------
// Fake transaction (satisfies pgx.Tx and therefore repository.Tx / DBTX)
// ---------------------------------------------------------------------------

// fakeTx is a no-op transaction used in service-layer unit tests. It
// satisfies pgx.Tx so it can be returned by fakePool.Begin and nested
// fakeTx.Begin (savepoints). The DBTX methods (Exec / QueryRow) panic
// because the fakeAuthRepo intercepts every data-access call before the
// service touches the underlying DBTX.
type fakeTx struct{}

// Compile-time check: fakeTx satisfies pgx.Tx (and therefore repository.Tx).
var _ pgx.Tx = (*fakeTx)(nil)

func (fakeTx) Begin(context.Context) (pgx.Tx, error) { return &fakeTx{}, nil }
func (fakeTx) Commit(context.Context) error          { return nil }
func (fakeTx) Rollback(context.Context) error        { return nil }

func (fakeTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	panic("fakeTx.Exec: unexpected call — repo fake should intercept all queries")
}

func (fakeTx) QueryRow(context.Context, string, ...any) pgx.Row {
	panic("fakeTx.QueryRow: unexpected call — repo fake should intercept all queries")
}

func (fakeTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	panic("fakeTx.Query: unexpected call — repo fake should intercept all queries")
}

func (fakeTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	panic("fakeTx.CopyFrom: unexpected call")
}

func (fakeTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	panic("fakeTx.SendBatch: unexpected call")
}

func (fakeTx) LargeObjects() pgx.LargeObjects {
	panic("fakeTx.LargeObjects: unexpected call")
}

func (fakeTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	panic("fakeTx.Prepare: unexpected call")
}

func (fakeTx) Conn() *pgx.Conn {
	panic("fakeTx.Conn: unexpected call")
}

// ---------------------------------------------------------------------------
// Fake pool (satisfies repository.Pool)
// ---------------------------------------------------------------------------

// fakePool is an in-memory Pool that returns fakeTx instances. The DBTX
// methods panic for the same reason as fakeTx: the fakeAuthRepo intercepts
// every query before the service touches the pool directly — except for
// methods like GetVerifiedUserByEmail which pass the pool as a DBTX.
// Because those calls are intercepted by the fake repo, Exec/QueryRow are
// never reached.
type fakePool struct{}

// Compile-time check: fakePool satisfies repository.Pool.
var _ repository.Pool = (*fakePool)(nil)

func (fakePool) Begin(context.Context) (pgx.Tx, error) { return &fakeTx{}, nil }

func (fakePool) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	panic("fakePool.Exec: unexpected call — repo fake should intercept all queries")
}

func (fakePool) QueryRow(context.Context, string, ...any) pgx.Row {
	panic("fakePool.QueryRow: unexpected call — repo fake should intercept all queries")
}

// ---------------------------------------------------------------------------
// Fake auth repository (satisfies repository.AuthRepository)
// ---------------------------------------------------------------------------

// fakeAuthRepo is a configurable in-memory implementation of
// repository.AuthRepository for service-layer unit tests. Each method
// delegates to a corresponding function field. Tests set only the fields
// they need; unset fields panic with a descriptive message so missing
// setup is caught immediately.
type fakeAuthRepo struct {
	// --- users ---
	getUserByEmailFn          func(ctx context.Context, db repository.DBTX, email string) (repository.UserRow, error)
	getUserByEmailForUpdateFn func(ctx context.Context, db repository.DBTX, email string) (repository.UserRow, error)
	getVerifiedUserByEmailFn  func(ctx context.Context, db repository.DBTX, email string) (repository.UserRow, error)
	getVerifiedUserByIDFn     func(ctx context.Context, db repository.DBTX, userID string) (repository.UserRow, error)
	getUserByIDFn             func(ctx context.Context, db repository.DBTX, userID string) (repository.UserRow, error)
	insertUserFn              func(ctx context.Context, db repository.DBTX, email, passwordHash, username string) (string, error)
	setEmailVerifiedFn        func(ctx context.Context, db repository.DBTX, userID string) error
	updatePasswordFn          func(ctx context.Context, db repository.DBTX, userID, passwordHash string) (int64, error)
	deleteUnverifiedUserFn    func(ctx context.Context, db repository.DBTX, userID string) (int64, error)
	updateUserFn              func(ctx context.Context, db repository.DBTX, userID string, name *string, username *string, leaderboardVisible *bool) (repository.UserRow, error)
	isUsernameTakenFn         func(ctx context.Context, db repository.DBTX, username string, excludeUserID string) (bool, error)
	deleteUserFn              func(ctx context.Context, db repository.DBTX, userID string) error

	// --- otp_codes ---
	insertOTPFn                  func(ctx context.Context, db repository.DBTX, userID, codeHash, purpose string, expiresAt time.Time) (string, error)
	getActiveOTPForUpdateFn      func(ctx context.Context, db repository.DBTX, userID, purpose string) (repository.OTPRow, error)
	incrementOTPAttemptsFn       func(ctx context.Context, db repository.DBTX, otpID string) error
	markOTPUsedFn                func(ctx context.Context, db repository.DBTX, otpID string) error
	deleteOTPsByUserAndPurposeFn func(ctx context.Context, db repository.DBTX, userID, purpose string) error
	deleteOTPByIDFn              func(ctx context.Context, db repository.DBTX, otpID string) error

	// --- refresh_tokens ---
	insertRefreshTokenFn             func(ctx context.Context, db repository.DBTX, userID, tokenHash string, expiresAt time.Time) error
	getRefreshTokenByHashForUpdateFn func(ctx context.Context, db repository.DBTX, tokenHash string) (repository.RefreshTokenRow, error)
	revokeRefreshTokenFn             func(ctx context.Context, db repository.DBTX, tokenID string) error
	revokeAllRefreshTokensFn         func(ctx context.Context, db repository.DBTX, userID string) error
	getUserEmailByIDFn               func(ctx context.Context, db repository.DBTX, userID string) (string, error)

	// --- subscriptions ---
	getActiveSubscriptionFn func(ctx context.Context, db repository.DBTX, userID string) (repository.SubscriptionRow, error)
}

// Compile-time check: fakeAuthRepo satisfies repository.AuthRepository.
var _ repository.AuthRepository = (*fakeAuthRepo)(nil)

func (f *fakeAuthRepo) GetUserByEmail(ctx context.Context, db repository.DBTX, email string) (repository.UserRow, error) {
	if f.getUserByEmailFn == nil {
		panic("fakeAuthRepo.GetUserByEmail: not configured")
	}
	return f.getUserByEmailFn(ctx, db, email)
}

func (f *fakeAuthRepo) GetUserByEmailForUpdate(ctx context.Context, db repository.DBTX, email string) (repository.UserRow, error) {
	if f.getUserByEmailForUpdateFn == nil {
		panic("fakeAuthRepo.GetUserByEmailForUpdate: not configured")
	}
	return f.getUserByEmailForUpdateFn(ctx, db, email)
}

func (f *fakeAuthRepo) GetVerifiedUserByEmail(ctx context.Context, db repository.DBTX, email string) (repository.UserRow, error) {
	if f.getVerifiedUserByEmailFn == nil {
		panic("fakeAuthRepo.GetVerifiedUserByEmail: not configured")
	}
	return f.getVerifiedUserByEmailFn(ctx, db, email)
}

func (f *fakeAuthRepo) GetVerifiedUserByID(ctx context.Context, db repository.DBTX, userID string) (repository.UserRow, error) {
	if f.getVerifiedUserByIDFn == nil {
		panic("fakeAuthRepo.GetVerifiedUserByID: not configured")
	}
	return f.getVerifiedUserByIDFn(ctx, db, userID)
}

func (f *fakeAuthRepo) GetUserByID(ctx context.Context, db repository.DBTX, userID string) (repository.UserRow, error) {
	if f.getUserByIDFn == nil {
		panic("fakeAuthRepo.GetUserByID: not configured")
	}
	return f.getUserByIDFn(ctx, db, userID)
}

func (f *fakeAuthRepo) InsertUser(ctx context.Context, db repository.DBTX, email, passwordHash, username string) (string, error) {
	if f.insertUserFn == nil {
		panic("fakeAuthRepo.InsertUser: not configured")
	}
	return f.insertUserFn(ctx, db, email, passwordHash, username)
}

func (f *fakeAuthRepo) SetEmailVerified(ctx context.Context, db repository.DBTX, userID string) error {
	if f.setEmailVerifiedFn == nil {
		panic("fakeAuthRepo.SetEmailVerified: not configured")
	}
	return f.setEmailVerifiedFn(ctx, db, userID)
}

func (f *fakeAuthRepo) UpdatePassword(ctx context.Context, db repository.DBTX, userID, passwordHash string) (int64, error) {
	if f.updatePasswordFn == nil {
		panic("fakeAuthRepo.UpdatePassword: not configured")
	}
	return f.updatePasswordFn(ctx, db, userID, passwordHash)
}

func (f *fakeAuthRepo) DeleteUnverifiedUser(ctx context.Context, db repository.DBTX, userID string) (int64, error) {
	if f.deleteUnverifiedUserFn == nil {
		panic("fakeAuthRepo.DeleteUnverifiedUser: not configured")
	}
	return f.deleteUnverifiedUserFn(ctx, db, userID)
}

func (f *fakeAuthRepo) InsertOTP(ctx context.Context, db repository.DBTX, userID, codeHash, purpose string, expiresAt time.Time) (string, error) {
	if f.insertOTPFn == nil {
		panic("fakeAuthRepo.InsertOTP: not configured")
	}
	return f.insertOTPFn(ctx, db, userID, codeHash, purpose, expiresAt)
}

func (f *fakeAuthRepo) GetActiveOTPForUpdate(ctx context.Context, db repository.DBTX, userID, purpose string) (repository.OTPRow, error) {
	if f.getActiveOTPForUpdateFn == nil {
		panic("fakeAuthRepo.GetActiveOTPForUpdate: not configured")
	}
	return f.getActiveOTPForUpdateFn(ctx, db, userID, purpose)
}

func (f *fakeAuthRepo) IncrementOTPAttempts(ctx context.Context, db repository.DBTX, otpID string) error {
	if f.incrementOTPAttemptsFn == nil {
		panic("fakeAuthRepo.IncrementOTPAttempts: not configured")
	}
	return f.incrementOTPAttemptsFn(ctx, db, otpID)
}

func (f *fakeAuthRepo) MarkOTPUsed(ctx context.Context, db repository.DBTX, otpID string) error {
	if f.markOTPUsedFn == nil {
		panic("fakeAuthRepo.MarkOTPUsed: not configured")
	}
	return f.markOTPUsedFn(ctx, db, otpID)
}

func (f *fakeAuthRepo) DeleteOTPsByUserAndPurpose(ctx context.Context, db repository.DBTX, userID, purpose string) error {
	if f.deleteOTPsByUserAndPurposeFn == nil {
		panic("fakeAuthRepo.DeleteOTPsByUserAndPurpose: not configured")
	}
	return f.deleteOTPsByUserAndPurposeFn(ctx, db, userID, purpose)
}

func (f *fakeAuthRepo) DeleteOTPByID(ctx context.Context, db repository.DBTX, otpID string) error {
	if f.deleteOTPByIDFn == nil {
		panic("fakeAuthRepo.DeleteOTPByID: not configured")
	}
	return f.deleteOTPByIDFn(ctx, db, otpID)
}

func (f *fakeAuthRepo) InsertRefreshToken(ctx context.Context, db repository.DBTX, userID, tokenHash string, expiresAt time.Time) error {
	if f.insertRefreshTokenFn == nil {
		panic("fakeAuthRepo.InsertRefreshToken: not configured")
	}
	return f.insertRefreshTokenFn(ctx, db, userID, tokenHash, expiresAt)
}

func (f *fakeAuthRepo) GetRefreshTokenByHashForUpdate(ctx context.Context, db repository.DBTX, tokenHash string) (repository.RefreshTokenRow, error) {
	if f.getRefreshTokenByHashForUpdateFn == nil {
		panic("fakeAuthRepo.GetRefreshTokenByHashForUpdate: not configured")
	}
	return f.getRefreshTokenByHashForUpdateFn(ctx, db, tokenHash)
}

func (f *fakeAuthRepo) RevokeRefreshToken(ctx context.Context, db repository.DBTX, tokenID string) error {
	if f.revokeRefreshTokenFn == nil {
		panic("fakeAuthRepo.RevokeRefreshToken: not configured")
	}
	return f.revokeRefreshTokenFn(ctx, db, tokenID)
}

func (f *fakeAuthRepo) RevokeAllRefreshTokens(ctx context.Context, db repository.DBTX, userID string) error {
	if f.revokeAllRefreshTokensFn == nil {
		panic("fakeAuthRepo.RevokeAllRefreshTokens: not configured")
	}
	return f.revokeAllRefreshTokensFn(ctx, db, userID)
}

func (f *fakeAuthRepo) GetUserEmailByID(ctx context.Context, db repository.DBTX, userID string) (string, error) {
	if f.getUserEmailByIDFn == nil {
		panic("fakeAuthRepo.GetUserEmailByID: not configured")
	}
	return f.getUserEmailByIDFn(ctx, db, userID)
}

func (f *fakeAuthRepo) GetActiveSubscription(ctx context.Context, db repository.DBTX, userID string) (repository.SubscriptionRow, error) {
	if f.getActiveSubscriptionFn == nil {
		panic("fakeAuthRepo.GetActiveSubscription: not configured")
	}
	return f.getActiveSubscriptionFn(ctx, db, userID)
}

func (f *fakeAuthRepo) UpdateUser(ctx context.Context, db repository.DBTX, userID string, name *string, username *string, leaderboardVisible *bool) (repository.UserRow, error) {
	if f.updateUserFn == nil {
		panic("fakeAuthRepo.UpdateUser: not configured")
	}
	return f.updateUserFn(ctx, db, userID, name, username, leaderboardVisible)
}

func (f *fakeAuthRepo) IsUsernameTaken(ctx context.Context, db repository.DBTX, username string, excludeUserID string) (bool, error) {
	if f.isUsernameTakenFn == nil {
		panic("fakeAuthRepo.IsUsernameTaken: not configured")
	}
	return f.isUsernameTakenFn(ctx, db, username, excludeUserID)
}

func (f *fakeAuthRepo) DeleteUser(ctx context.Context, db repository.DBTX, userID string) error {
	if f.deleteUserFn == nil {
		panic("fakeAuthRepo.DeleteUser: not configured")
	}
	return f.deleteUserFn(ctx, db, userID)
}

// ---------------------------------------------------------------------------
// Fake mail sender (satisfies mail.Sender)
// ---------------------------------------------------------------------------

// fakeSender is an in-memory mail.Sender that records calls for later
// assertion. If sendOTPFn is set it is called; otherwise SendOTP succeeds
// with nil and the call is recorded.
type fakeSender struct {
	mu        sync.Mutex
	calls     []fakeSendOTPCall
	sendOTPFn func(ctx context.Context, to, otp, purpose string) error
}

// fakeSendOTPCall records the arguments of a single SendOTP invocation.
type fakeSendOTPCall struct {
	To      string
	OTP     string
	Purpose string
}

// Compile-time check: fakeSender satisfies mail.Sender.
var _ mail.Sender = (*fakeSender)(nil)

func (f *fakeSender) SendOTP(ctx context.Context, to, otp, purpose string) error {
	f.mu.Lock()
	f.calls = append(f.calls, fakeSendOTPCall{To: to, OTP: otp, Purpose: purpose})
	f.mu.Unlock()

	if f.sendOTPFn != nil {
		return f.sendOTPFn(ctx, to, otp, purpose)
	}
	return nil
}

// sendOTPCalls returns a snapshot of all recorded SendOTP calls.
func (f *fakeSender) sendOTPCalls() []fakeSendOTPCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeSendOTPCall, len(f.calls))
	copy(out, f.calls)
	return out
}
