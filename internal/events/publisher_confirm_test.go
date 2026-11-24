package events

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeConfirm struct {
	acked bool
	err   error
	block bool
}

func (f fakeConfirm) WaitContext(ctx context.Context) (bool, error) {
	if f.block {
		<-ctx.Done()
		return false, ctx.Err()
	}
	return f.acked, f.err
}

func TestWaitConfirm(t *testing.T) {
	boom := errors.New("canal cerrado")
	tests := []struct {
		name    string
		confirm fakeConfirm
		wantErr bool
		wantIs  error
	}{
		{name: "ack del broker", confirm: fakeConfirm{acked: true}},
		{name: "nack del broker", confirm: fakeConfirm{acked: false}, wantErr: true},
		{name: "error de canal", confirm: fakeConfirm{err: boom}, wantErr: true, wantIs: boom},
		{name: "el broker nunca responde", confirm: fakeConfirm{block: true}, wantErr: true, wantIs: context.DeadlineExceeded},
	}
	old := confirmTimeout
	confirmTimeout = 20 * time.Millisecond
	defer func() { confirmTimeout = old }()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := waitConfirm(context.Background(), tt.confirm)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Fatalf("err = %v, want que envuelva %v", err, tt.wantIs)
			}
		})
	}
}
