package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/events-contract/admin"
)

const (
	recoveryTestSecret    = "secreto"
	recoveryTestRequestID = "5b2d7c1e-3a9f-4e8b-b6c4-1f2e3d4c5b6a"
	recoveryTestUserID    = "7c1e5a3b-9d2f-4b8e-a6c4-3f1e9d7b5a2c"
	recoveryTestStaffID   = "11111111-1111-1111-1111-111111111111"
)

type fakeRecoveryRepo struct {
	filter     repository.RecoveryRequestFilter
	limit      int
	resolvedID string
	staffID    string
	status     model.RecoveryRequestStatus
	key        string
	payload    any
	err        error
}

func (f *fakeRecoveryRepo) Insert(context.Context, repository.RecoveryRequestRecord) error {
	return f.err
}

func (f *fakeRecoveryRepo) List(_ context.Context, filter repository.RecoveryRequestFilter, limit, _ int) ([]model.RecoveryRequest, int, error) {
	f.filter, f.limit = filter, limit
	return []model.RecoveryRequest{}, 0, f.err
}

func (f *fakeRecoveryRepo) Get(_ context.Context, id string) (*model.RecoveryRequestDetail, error) {
	return &model.RecoveryRequestDetail{RecoveryRequest: model.RecoveryRequest{ID: id}}, f.err
}

func (f *fakeRecoveryRepo) Resolve(_ context.Context, id, staffID string, status model.RecoveryRequestStatus, _ time.Time, event repository.RecoveryEventBuilder) (*model.RecoveryRequestDetail, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.resolvedID, f.staffID, f.status = id, staffID, status
	f.key, f.payload = event(recoveryTestUserID)
	return &model.RecoveryRequestDetail{RecoveryRequest: model.RecoveryRequest{ID: id, Status: status}}, nil
}

func (f *fakeRecoveryRepo) UserRequests(context.Context, string, int, int) ([]model.RecoveryRequest, int, error) {
	return nil, 0, f.err
}

func TestRecoveryApproveSignsTheEventForTheRequestOwner(t *testing.T) {
	repo := &fakeRecoveryRepo{}
	svc := NewRecoveryService(repo, recoveryTestSecret)

	// El id llega en mayúsculas y se normaliza.
	if _, err := svc.Approve(context.Background(), "5B2D7C1E-3A9F-4E8B-B6C4-1F2E3D4C5B6A", recoveryTestStaffID); err != nil {
		t.Fatal(err)
	}
	if repo.resolvedID != recoveryTestRequestID || repo.staffID != recoveryTestStaffID || repo.status != model.RecoveryRequestApproved {
		t.Errorf("resolve(%s, %s, %s)", repo.resolvedID, repo.staffID, repo.status)
	}
	event, ok := repo.payload.(admin.AccountRecoveryApproved)
	if repo.key != admin.RoutingKeyAccountRecoveryApproved || !ok {
		t.Fatalf("evento = %q %T", repo.key, repo.payload)
	}
	if event.RequestID != recoveryTestRequestID || event.UserID != recoveryTestUserID || !event.Verify(recoveryTestSecret) {
		t.Errorf("evento = %+v (verifica=%v)", event, event.Verify(recoveryTestSecret))
	}
}

func TestRecoveryRejectSignsARejection(t *testing.T) {
	repo := &fakeRecoveryRepo{}
	if _, err := NewRecoveryService(repo, recoveryTestSecret).Reject(context.Background(), recoveryTestRequestID, recoveryTestStaffID); err != nil {
		t.Fatal(err)
	}
	event, ok := repo.payload.(admin.AccountRecoveryRejected)
	if repo.status != model.RecoveryRequestRejected || repo.key != admin.RoutingKeyAccountRecoveryRejected || !ok || !event.Verify(recoveryTestSecret) {
		t.Errorf("rechazo: status=%s key=%q evento=%+v", repo.status, repo.key, repo.payload)
	}
}

func TestRecoveryInvalidIDIsNotFound(t *testing.T) {
	svc := NewRecoveryService(&fakeRecoveryRepo{}, recoveryTestSecret)
	if _, err := svc.Approve(context.Background(), "no-es-un-uuid", recoveryTestStaffID); !errors.Is(err, ErrRecoveryRequestNotFound) {
		t.Errorf("Approve = %v", err)
	}
	if _, err := svc.Get(context.Background(), "no-es-un-uuid"); !errors.Is(err, ErrRecoveryRequestNotFound) {
		t.Errorf("Get = %v", err)
	}
}

func TestRecoveryResolveKeepsRepositoryErrors(t *testing.T) {
	svc := NewRecoveryService(&fakeRecoveryRepo{err: repository.ErrRecoveryRequestResolved}, recoveryTestSecret)
	if _, err := svc.Reject(context.Background(), recoveryTestRequestID, recoveryTestStaffID); !errors.Is(err, ErrRecoveryRequestResolved) {
		t.Errorf("Reject = %v", err)
	}
}

func TestRecoveryListValidatesAndNormalizes(t *testing.T) {
	repo := &fakeRecoveryRepo{}
	svc := NewRecoveryService(repo, recoveryTestSecret)

	if _, err := svc.List(context.Background(), ListRecoveryRequestsParams{Status: "otra"}); !errors.Is(err, ErrInvalidRecoveryStatus) {
		t.Errorf("estado inválido: %v", err)
	}
	list, err := svc.List(context.Background(), ListRecoveryRequestsParams{Search: "ana", Status: model.RecoveryRequestPending, Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	if repo.limit != model.MaxLimit || list.Limit != model.MaxLimit || repo.filter.Search != "ana" || repo.filter.Status != model.RecoveryRequestPending {
		t.Errorf("filtro=%+v limit=%d", repo.filter, repo.limit)
	}
}
