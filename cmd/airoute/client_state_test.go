package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zbss/airoute/internal/clientauth"
	"github.com/zbss/airoute/internal/clientstore"
)

func TestClientStateBackupVerifyAndRestoreCommands(t *testing.T) {
	runtimeDirectory := t.TempDir()
	t.Setenv("AIROUTE_RUNTIME_DIR", runtimeDirectory)
	t.Setenv("AIROUTE_CREDENTIAL_MASTER_KEY", "")
	t.Setenv("AIROUTE_CREDENTIAL_PREVIOUS_KEYS", "")
	databasePath := filepath.Join(runtimeDirectory, "gateway-state.db")
	masterPath := filepath.Join(runtimeDirectory, "credential-master.key")
	keys, err := clientauth.LoadOrCreateKeyRing(masterPath)
	if err != nil {
		t.Fatal(err)
	}
	store, err := clientstore.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	policy := clientstore.ClientPolicy{ID: "policy_a"}
	client := clientstore.Client{ID: "client_a", Name: "Client", Status: clientstore.ClientActive, PolicyID: policy.ID}
	if err = store.CreateClient(context.Background(), clientstore.DefaultScope, client, policy); err != nil {
		t.Fatal(err)
	}
	credential, _, err := keys.Generate(client.ID, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.CreateCredential(context.Background(), clientstore.DefaultScope, credential); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "gateway-state.db")
	if err = clientStateCommand([]string{"backup", "--output", backup}); err != nil {
		t.Fatal(err)
	}
	if err = clientStateCommand([]string{"verify", "--backup", backup}); err != nil {
		t.Fatal(err)
	}
	changed, err := clientstore.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	client, err = changed.GetClient(context.Background(), clientstore.DefaultScope, client.ID)
	if err != nil {
		t.Fatal(err)
	}
	client.Status = clientstore.ClientDeleted
	if err = changed.UpdateClient(context.Background(), clientstore.DefaultScope, client); err != nil {
		t.Fatal(err)
	}
	if err = changed.Close(); err != nil {
		t.Fatal(err)
	}
	if err = clientStateCommand([]string{"restore", "--backup", backup}); err != nil {
		t.Fatal(err)
	}
	restored, err := clientstore.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	client, err = restored.GetClient(context.Background(), clientstore.DefaultScope, "client_a")
	if err != nil || client.Status != clientstore.ClientActive {
		t.Fatalf("backup was not restored: %#v %v", client, err)
	}
}
