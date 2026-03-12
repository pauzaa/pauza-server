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

type fakeTx struct{}

var _ pgx.Tx = (*fakeTx)(nil)

func (fakeTx) Begin(context.Context) (pgx.Tx, error) { return &fakeTx{}, nil }
func (fakeTx) Commit(context.Context) error          { return nil }
func (fakeTx) Rollback(context.Context) error        { return nil }
func (fakeTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	panic("fakeTx.Exec: unexpected call")
}
func (fakeTx) QueryRow(context.Context, string, ...any) pgx.Row {
	panic("fakeTx.QueryRow: unexpected call")
}
func (fakeTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	panic("fakeTx.Query: unexpected call")
}
func (fakeTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	panic("fakeTx.CopyFrom: unexpected call")
}
func (fakeTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	panic("fakeTx.SendBatch: unexpected call")
}
func (fakeTx) LargeObjects() pgx.LargeObjects { panic("fakeTx.LargeObjects: unexpected call") }
func (fakeTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	panic("fakeTx.Prepare: unexpected call")
}
func (fakeTx) Conn() *pgx.Conn { panic("fakeTx.Conn: unexpected call") }

type fakePool struct {
	beginFn func(ctx context.Context) (pgx.Tx, error)
}

var _ repository.Pool = (*fakePool)(nil)

func (f *fakePool) Begin(ctx context.Context) (pgx.Tx, error) {
	if f != nil && f.beginFn != nil {
		return f.beginFn(ctx)
	}
	return &fakeTx{}, nil
}
func (fakePool) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	panic("fakePool.Exec: unexpected call")
}
func (fakePool) QueryRow(context.Context, string, ...any) pgx.Row {
	panic("fakePool.QueryRow: unexpected call")
}
func (fakePool) Query(context.Context, string, ...any) (pgx.Rows, error) {
	panic("fakePool.Query: unexpected call")
}

type fakeAuthRepo struct {
	getUserByEmailFn                          func(ctx context.Context, db repository.DBTX, email string) (repository.UserRow, error)
	getUserByEmailForUpdateFn                 func(ctx context.Context, db repository.DBTX, email string) (repository.UserRow, error)
	getUserByIDFn                             func(ctx context.Context, db repository.DBTX, userID string) (repository.UserRow, error)
	getUserByIDForUpdateFn                    func(ctx context.Context, db repository.DBTX, userID string) (repository.UserRow, error)
	insertUserFn                              func(ctx context.Context, db repository.DBTX, email, username string) (string, error)
	updateUserFn                              func(ctx context.Context, db repository.DBTX, userID string, name *string, username *string, leaderboardVisible *bool, profilePictureURL *string) (repository.UserRow, error)
	getPushEnabledFn                          func(ctx context.Context, db repository.DBTX, userID string) (bool, error)
	updatePushEnabledFn                       func(ctx context.Context, db repository.DBTX, userID string, pushEnabled bool) (bool, error)
	isUsernameTakenFn                         func(ctx context.Context, db repository.DBTX, username string, excludeUserID string) (bool, error)
	deleteUserFn                              func(ctx context.Context, db repository.DBTX, userID string) error
	insertOTPFn                               func(ctx context.Context, db repository.DBTX, email string, userID *string, codeHash, purpose string, expiresAt time.Time) (string, error)
	getActiveOTPForUpdateFn                   func(ctx context.Context, db repository.DBTX, email, purpose string) (repository.OTPRow, error)
	countFailedOTPAttemptsSinceForUpdateFn    func(ctx context.Context, db repository.DBTX, email, purpose string, since time.Time) (int, error)
	getOldestFailedOTPAttemptSinceForUpdateFn func(ctx context.Context, db repository.DBTX, email, purpose string, since time.Time) (time.Time, error)
	insertFailedOTPAttemptFn                  func(ctx context.Context, db repository.DBTX, email string, userID *string, purpose string, attemptedAt time.Time) error
	markOTPUsedFn                             func(ctx context.Context, db repository.DBTX, otpID string) error
	deleteOTPsByEmailAndPurposeFn             func(ctx context.Context, db repository.DBTX, email, purpose string) error
	deleteOTPByIDFn                           func(ctx context.Context, db repository.DBTX, otpID string) error
	insertRefreshTokenFn                      func(ctx context.Context, db repository.DBTX, userID, tokenHash string, expiresAt time.Time) error
	getRefreshTokenByHashForUpdateFn          func(ctx context.Context, db repository.DBTX, tokenHash string) (repository.RefreshTokenRow, error)
	revokeRefreshTokenFn                      func(ctx context.Context, db repository.DBTX, tokenID string) error
	revokeAllRefreshTokensFn                  func(ctx context.Context, db repository.DBTX, userID string) error
	getUserEmailByIDFn                        func(ctx context.Context, db repository.DBTX, userID string) (string, error)
	getEntitlementSnapshotFn                  func(ctx context.Context, db repository.DBTX, userID string) (repository.EntitlementRow, error)
}

var _ repository.AuthRepository = (*fakeAuthRepo)(nil)

func (f *fakeAuthRepo) GetUserByEmail(ctx context.Context, db repository.DBTX, email string) (repository.UserRow, error) {
	return f.getUserByEmailFn(ctx, db, email)
}
func (f *fakeAuthRepo) GetUserByEmailForUpdate(ctx context.Context, db repository.DBTX, email string) (repository.UserRow, error) {
	return f.getUserByEmailForUpdateFn(ctx, db, email)
}
func (f *fakeAuthRepo) GetUserByID(ctx context.Context, db repository.DBTX, userID string) (repository.UserRow, error) {
	return f.getUserByIDFn(ctx, db, userID)
}
func (f *fakeAuthRepo) GetUserByIDForUpdate(ctx context.Context, db repository.DBTX, userID string) (repository.UserRow, error) {
	return f.getUserByIDForUpdateFn(ctx, db, userID)
}
func (f *fakeAuthRepo) InsertUser(ctx context.Context, db repository.DBTX, email, username string) (string, error) {
	return f.insertUserFn(ctx, db, email, username)
}
func (f *fakeAuthRepo) UpdateUser(ctx context.Context, db repository.DBTX, userID string, name *string, username *string, leaderboardVisible *bool, profilePictureURL *string) (repository.UserRow, error) {
	return f.updateUserFn(ctx, db, userID, name, username, leaderboardVisible, profilePictureURL)
}
func (f *fakeAuthRepo) GetPushEnabled(ctx context.Context, db repository.DBTX, userID string) (bool, error) {
	return f.getPushEnabledFn(ctx, db, userID)
}
func (f *fakeAuthRepo) UpdatePushEnabled(ctx context.Context, db repository.DBTX, userID string, pushEnabled bool) (bool, error) {
	return f.updatePushEnabledFn(ctx, db, userID, pushEnabled)
}
func (f *fakeAuthRepo) IsUsernameTaken(ctx context.Context, db repository.DBTX, username string, excludeUserID string) (bool, error) {
	return f.isUsernameTakenFn(ctx, db, username, excludeUserID)
}
func (f *fakeAuthRepo) DeleteUser(ctx context.Context, db repository.DBTX, userID string) error {
	return f.deleteUserFn(ctx, db, userID)
}
func (f *fakeAuthRepo) InsertOTP(ctx context.Context, db repository.DBTX, email string, userID *string, codeHash, purpose string, expiresAt time.Time) (string, error) {
	return f.insertOTPFn(ctx, db, email, userID, codeHash, purpose, expiresAt)
}
func (f *fakeAuthRepo) GetActiveOTPForUpdate(ctx context.Context, db repository.DBTX, email, purpose string) (repository.OTPRow, error) {
	return f.getActiveOTPForUpdateFn(ctx, db, email, purpose)
}
func (f *fakeAuthRepo) CountFailedOTPAttemptsSinceForUpdate(ctx context.Context, db repository.DBTX, email, purpose string, since time.Time) (int, error) {
	return f.countFailedOTPAttemptsSinceForUpdateFn(ctx, db, email, purpose, since)
}
func (f *fakeAuthRepo) GetOldestFailedOTPAttemptSinceForUpdate(ctx context.Context, db repository.DBTX, email, purpose string, since time.Time) (time.Time, error) {
	return f.getOldestFailedOTPAttemptSinceForUpdateFn(ctx, db, email, purpose, since)
}
func (f *fakeAuthRepo) InsertFailedOTPAttempt(ctx context.Context, db repository.DBTX, email string, userID *string, purpose string, attemptedAt time.Time) error {
	return f.insertFailedOTPAttemptFn(ctx, db, email, userID, purpose, attemptedAt)
}
func (f *fakeAuthRepo) MarkOTPUsed(ctx context.Context, db repository.DBTX, otpID string) error {
	return f.markOTPUsedFn(ctx, db, otpID)
}
func (f *fakeAuthRepo) DeleteOTPsByEmailAndPurpose(ctx context.Context, db repository.DBTX, email, purpose string) error {
	return f.deleteOTPsByEmailAndPurposeFn(ctx, db, email, purpose)
}
func (f *fakeAuthRepo) DeleteOTPByID(ctx context.Context, db repository.DBTX, otpID string) error {
	return f.deleteOTPByIDFn(ctx, db, otpID)
}
func (f *fakeAuthRepo) InsertRefreshToken(ctx context.Context, db repository.DBTX, userID, tokenHash string, expiresAt time.Time) error {
	return f.insertRefreshTokenFn(ctx, db, userID, tokenHash, expiresAt)
}
func (f *fakeAuthRepo) GetRefreshTokenByHashForUpdate(ctx context.Context, db repository.DBTX, tokenHash string) (repository.RefreshTokenRow, error) {
	return f.getRefreshTokenByHashForUpdateFn(ctx, db, tokenHash)
}
func (f *fakeAuthRepo) RevokeRefreshToken(ctx context.Context, db repository.DBTX, tokenID string) error {
	return f.revokeRefreshTokenFn(ctx, db, tokenID)
}
func (f *fakeAuthRepo) RevokeAllRefreshTokens(ctx context.Context, db repository.DBTX, userID string) error {
	return f.revokeAllRefreshTokensFn(ctx, db, userID)
}
func (f *fakeAuthRepo) GetUserEmailByID(ctx context.Context, db repository.DBTX, userID string) (string, error) {
	return f.getUserEmailByIDFn(ctx, db, userID)
}
func (f *fakeAuthRepo) GetEntitlementSnapshot(ctx context.Context, db repository.DBTX, userID string) (repository.EntitlementRow, error) {
	if f.getEntitlementSnapshotFn == nil {
		return repository.EntitlementRow{}, repository.ErrNotFound
	}
	return f.getEntitlementSnapshotFn(ctx, db, userID)
}

type fakeSendOTPCall struct {
	To      string
	OTP     string
	Purpose string
}

type fakeSender struct {
	mu        sync.Mutex
	calls     []fakeSendOTPCall
	probeFn   func(ctx context.Context) error
	sendOTPFn func(ctx context.Context, to, otp, purpose string) error
}

var _ mail.Sender = (*fakeSender)(nil)

func (f *fakeSender) Probe(ctx context.Context) error {
	if f.probeFn != nil {
		return f.probeFn(ctx)
	}
	return nil
}

func (f *fakeSender) SendOTP(ctx context.Context, to, otp, purpose string) error {
	f.mu.Lock()
	f.calls = append(f.calls, fakeSendOTPCall{To: to, OTP: otp, Purpose: purpose})
	f.mu.Unlock()
	if f.sendOTPFn != nil {
		return f.sendOTPFn(ctx, to, otp, purpose)
	}
	return nil
}

func (f *fakeSender) sendOTPCalls() []fakeSendOTPCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeSendOTPCall, len(f.calls))
	copy(out, f.calls)
	return out
}
