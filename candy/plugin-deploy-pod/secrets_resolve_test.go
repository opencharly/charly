package deploypod

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/spec/proto"
	"google.golang.org/grpc"
)

// secrets_resolve_test.go — coverage for the quadlet autostart CAPABILITY the secret layer
// computes. The emitters' two arms are proven in sdk/deploykit's quadlet_test.go; what is proven
// HERE is the half that can silently fail with correct emitters — that this plugin actually asks
// the credential store, and asks it only when there is something to unlock.

// fakeCredExecutorServiceClient answers InvokeProvider(verb:credential) with a canned resolve
// reply and records that it was reached. Every other RPC panics: this path must not touch them.
type fakeCredExecutorServiceClient struct {
	pb.ExecutorServiceClient
	reply  string // JSON credentialViaExecReply
	called int
}

func (f *fakeCredExecutorServiceClient) InvokeProvider(_ context.Context, in *pb.InvokeProviderRequest, _ ...grpc.CallOption) (*pb.InvokeReply, error) {
	f.called++
	return &pb.InvokeReply{ResultJson: []byte(f.reply)}, nil
}

func (f *fakeCredExecutorServiceClient) HostBuild(_ context.Context, _ *pb.HostBuildRequest, _ ...grpc.CallOption) (*pb.HostBuildReply, error) {
	panic("HostBuild must not be reached while computing the unattended-unlock capability")
}

func credReply(t *testing.T, value, source string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"value": value, "source": source})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestResolveEncUnattendedUnlock covers both arms of the capability plus the short-circuit. The
// source rows mirror what the boot path will see, so a deploy is enabled at boot exactly when
// its passphrase can be obtained without a human.
func TestResolveEncUnattendedUnlock(t *testing.T) {
	cases := []struct {
		name         string
		hasEncrypted bool
		value        string
		source       string
		want         bool
		wantCalls    int
	}{
		{"no encrypted volumes never consults the credential store", false, "", "", false, 0},
		{"passphrase in hand is unattended-capable", true, "the-secret", "keyring", true, 1},
		{"locked keyring is unattended-capable — it unlocks at login", true, "", "locked", true, 1},
		{"stored nowhere is human-gated", true, "", "default", false, 1},
		{"unprobeable backend is human-gated", true, "", "unavailable", false, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeCredExecutorServiceClient{reply: credReply(t, tc.value, tc.source)}
			ex := sdk.NewInProcExecutor(fake)

			got := resolveEncUnattendedUnlock(context.Background(), ex, "myapp", tc.hasEncrypted)

			if got != tc.want {
				t.Errorf("resolveEncUnattendedUnlock(hasEncrypted=%v, source=%q) = %v, want %v",
					tc.hasEncrypted, tc.source, got, tc.want)
			}
			// The call count is the wiring assertion: a capability that never asks the credential
			// store would still return the right answer for two of these rows by accident.
			if fake.called != tc.wantCalls {
				t.Errorf("credential store consulted %d time(s), want %d", fake.called, tc.wantCalls)
			}
		})
	}
}
