package clientstore

import (
	"context"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

func clientInScope(client Client, scope Scope) bool {
	scope = scope.Normalize()
	return client.TenantID == scope.TenantID && client.ProjectID == scope.ProjectID
}

func (s *BoltStore) CreateClient(_ context.Context, scope Scope, client Client, policy ClientPolicy) error {
	scope = scope.Normalize()
	now := time.Now().UTC()
	client.ID = strings.TrimSpace(client.ID)
	client.Name = strings.TrimSpace(client.Name)
	client.PolicyID = strings.TrimSpace(client.PolicyID)
	if client.ID == "" || client.Name == "" || client.PolicyID == "" {
		return errors.New("client id, name and policy id are required")
	}
	if client.Status == "" {
		client.Status = ClientActive
	}
	if client.Status != ClientActive && client.Status != ClientDisabled {
		return errors.New("new client status must be active or disabled")
	}
	client.TenantID, client.ProjectID = scope.TenantID, scope.ProjectID
	if client.CreatedAt.IsZero() {
		client.CreatedAt = now
	}
	client.UpdatedAt = now
	policy.ID = client.PolicyID
	policy.ProjectID = scope.ProjectID
	if err := policy.Validate(); err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		key := scopedKey(scope, client.ID)
		if tx.Bucket(bucketClients).Get(key) != nil {
			return ErrAlreadyExists
		}
		if tx.Bucket(bucketPolicies).Get(scopedKey(scope, policy.ID)) != nil {
			return ErrAlreadyExists
		}
		if err := put(tx.Bucket(bucketClients), key, client); err != nil {
			return err
		}
		return put(tx.Bucket(bucketPolicies), scopedKey(scope, policy.ID), policy)
	})
}

func validClientTransition(from, to ClientStatus) bool {
	if from == to {
		return true
	}
	if from == ClientDeleted {
		return false
	}
	switch to {
	case ClientActive, ClientDisabled, ClientDeleted:
		return true
	default:
		return false
	}
}

func (s *BoltStore) UpdateClient(_ context.Context, scope Scope, client Client) error {
	scope = scope.Normalize()
	return s.db.Update(func(tx *bolt.Tx) error {
		key := scopedKey(scope, client.ID)
		var existing Client
		if err := decode(tx.Bucket(bucketClients).Get(key), &existing); err != nil {
			return err
		}
		if !clientInScope(existing, scope) || !validClientTransition(existing.Status, client.Status) {
			return ErrInvalidState
		}
		if strings.TrimSpace(client.Name) == "" || client.PolicyID != existing.PolicyID {
			return errors.New("client name is required and policy id cannot change")
		}
		client.TenantID, client.ProjectID = scope.TenantID, scope.ProjectID
		client.CreatedAt = existing.CreatedAt
		client.UpdatedAt = time.Now().UTC()
		return put(tx.Bucket(bucketClients), key, client)
	})
}

func (s *BoltStore) GetClient(_ context.Context, scope Scope, id string) (Client, error) {
	scope = scope.Normalize()
	var client Client
	err := s.db.View(func(tx *bolt.Tx) error {
		return decode(tx.Bucket(bucketClients).Get(scopedKey(scope, id)), &client)
	})
	if err == nil && !clientInScope(client, scope) {
		err = ErrNotFound
	}
	return client, err
}

func (s *BoltStore) ListClients(ctx context.Context, scope Scope, filter ClientFilter) ([]ClientSummary, error) {
	scope = scope.Normalize()
	clients := make([]Client, 0)
	err := s.db.View(func(tx *bolt.Tx) error {
		cursor := tx.Bucket(bucketClients).Cursor()
		prefix := scopedPrefix(scope)
		for key, raw := cursor.Seek(prefix); key != nil && strings.HasPrefix(string(key), string(prefix)); key, raw = cursor.Next() {
			var client Client
			if err := decode(raw, &client); err != nil {
				return err
			}
			if (!filter.IncludeDeleted && client.Status == ClientDeleted) || (filter.Status != "" && client.Status != filter.Status) {
				continue
			}
			if search := strings.ToLower(strings.TrimSpace(filter.Search)); search != "" && !strings.Contains(strings.ToLower(client.Name+" "+client.Description), search) {
				continue
			}
			clients = append(clients, client)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(clients, func(i, j int) bool { return clients[i].CreatedAt.Before(clients[j].CreatedAt) })
	out := make([]ClientSummary, 0, len(clients))
	for _, client := range clients {
		policy, err := s.GetPolicy(ctx, scope, client.PolicyID)
		if err != nil {
			return nil, err
		}
		credentials, err := s.ListCredentials(ctx, scope, client.ID)
		if err != nil {
			return nil, err
		}
		views := make([]CredentialView, 0, len(credentials))
		active := 0
		for _, credential := range credentials {
			views = append(views, credential.View())
			if credential.Status == CredentialActive && (credential.ExpiresAt == nil || credential.ExpiresAt.After(time.Now())) {
				active++
			}
		}
		usage, err := s.GetUsage(ctx, scope, client.ID, UsageQuery{From: startOfDay(time.Now()), To: time.Now()})
		if err != nil {
			return nil, err
		}
		today := UsageBucket{}
		if len(usage.Daily) > 0 {
			today = usage.Daily[len(usage.Daily)-1]
		}
		out = append(out, ClientSummary{Client: client, Policy: policy, Credentials: views, ActiveCredentials: active, Today: today})
	}
	return out, nil
}

func (s *BoltStore) CreateCredential(_ context.Context, scope Scope, credential Credential) error {
	scope = scope.Normalize()
	credential.ID = strings.TrimSpace(credential.ID)
	credential.ClientID = strings.TrimSpace(credential.ClientID)
	credential.Prefix = strings.TrimSpace(credential.Prefix)
	if credential.ID == "" || credential.ClientID == "" || credential.Prefix == "" || len(credential.SecretHMAC) == 0 || credential.HMACKeyID == "" {
		return errors.New("credential id, client id, prefix, HMAC and HMAC key id are required")
	}
	if credential.Kind == "" {
		credential.Kind = CredentialStandard
	}
	if credential.Kind != CredentialStandard && credential.Kind != CredentialManaged {
		return errors.New("unsupported credential kind")
	}
	if credential.Kind == CredentialManaged && (len(credential.SecretCiphertext) == 0 || len(credential.SecretNonce) == 0) {
		return errors.New("managed credential requires encrypted secret material")
	}
	if credential.Status == "" {
		credential.Status = CredentialActive
	}
	if credential.Status != CredentialActive && credential.Status != CredentialDisabled {
		return errors.New("new credential status must be active or disabled")
	}
	if credential.CreatedAt.IsZero() {
		credential.CreatedAt = time.Now().UTC()
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		var client Client
		if err := decode(tx.Bucket(bucketClients).Get(scopedKey(scope, credential.ClientID)), &client); err != nil {
			return err
		}
		if client.Status == ClientDeleted || !clientInScope(client, scope) {
			return ErrInvalidState
		}
		key := scopedKey(scope, credential.ID)
		if tx.Bucket(bucketCredentials).Get(key) != nil || tx.Bucket(bucketCredentialByPrefix).Get(scopedKey(scope, credential.Prefix)) != nil {
			return ErrAlreadyExists
		}
		if err := put(tx.Bucket(bucketCredentials), key, credential); err != nil {
			return err
		}
		if err := tx.Bucket(bucketCredentialByPrefix).Put(scopedKey(scope, credential.Prefix), []byte(credential.ID)); err != nil {
			return err
		}
		hmacIndex := credential.HMACKeyID + "\x00" + hex.EncodeToString(credential.SecretHMAC)
		if tx.Bucket(bucketCredentialByHMAC).Get(scopedKey(scope, hmacIndex)) != nil {
			return ErrAlreadyExists
		}
		return tx.Bucket(bucketCredentialByHMAC).Put(scopedKey(scope, hmacIndex), []byte(credential.ID))
	})
}

func (s *BoltStore) GetCredential(_ context.Context, scope Scope, id string) (Credential, error) {
	scope = scope.Normalize()
	var credential Credential
	err := s.db.View(func(tx *bolt.Tx) error {
		if err := decode(tx.Bucket(bucketCredentials).Get(scopedKey(scope, id)), &credential); err != nil {
			return err
		}
		var client Client
		return decode(tx.Bucket(bucketClients).Get(scopedKey(scope, credential.ClientID)), &client)
	})
	return credential, err
}

func (s *BoltStore) GetCredentialByHMAC(ctx context.Context, scope Scope, keyID string, digest []byte) (Credential, error) {
	scope = scope.Normalize()
	var credentialID string
	err := s.db.View(func(tx *bolt.Tx) error {
		index := keyID + "\x00" + hex.EncodeToString(digest)
		raw := tx.Bucket(bucketCredentialByHMAC).Get(scopedKey(scope, index))
		if len(raw) == 0 {
			return ErrNotFound
		}
		credentialID = string(raw)
		return nil
	})
	if err != nil {
		return Credential{}, err
	}
	return s.GetCredential(ctx, scope, credentialID)
}

func (s *BoltStore) ListCredentials(_ context.Context, scope Scope, clientID string) ([]Credential, error) {
	scope = scope.Normalize()
	out := make([]Credential, 0)
	err := s.db.View(func(tx *bolt.Tx) error {
		cursor := tx.Bucket(bucketCredentials).Cursor()
		prefix := scopedPrefix(scope)
		for key, raw := cursor.Seek(prefix); key != nil && strings.HasPrefix(string(key), string(prefix)); key, raw = cursor.Next() {
			var credential Credential
			if err := decode(raw, &credential); err != nil {
				return err
			}
			if credential.ClientID == clientID {
				out = append(out, credential)
			}
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, err
}

func validCredentialTransition(from, to CredentialStatus) bool {
	if from == to {
		return true
	}
	if from == CredentialRevoked || from == CredentialExpired {
		return false
	}
	switch to {
	case CredentialActive, CredentialDisabled, CredentialExpired, CredentialRevoked:
		return true
	default:
		return false
	}
}

func (s *BoltStore) UpdateCredentialStatus(_ context.Context, scope Scope, id string, status CredentialStatus, now time.Time) error {
	scope = scope.Normalize()
	return s.db.Update(func(tx *bolt.Tx) error {
		key := scopedKey(scope, id)
		var credential Credential
		if err := decode(tx.Bucket(bucketCredentials).Get(key), &credential); err != nil {
			return err
		}
		if !validCredentialTransition(credential.Status, status) {
			return ErrInvalidState
		}
		credential.Status = status
		if status == CredentialRevoked {
			value := now.UTC()
			credential.RevokedAt = &value
		}
		return put(tx.Bucket(bucketCredentials), key, credential)
	})
}

func (s *BoltStore) DeleteCredential(_ context.Context, scope Scope, id string) error {
	scope = scope.Normalize()
	return s.db.Update(func(tx *bolt.Tx) error {
		key := scopedKey(scope, id)
		var credential Credential
		if err := decode(tx.Bucket(bucketCredentials).Get(key), &credential); err != nil {
			return err
		}
		if status := credential.View().Status; status != CredentialRevoked && status != CredentialExpired {
			return ErrInvalidState
		}
		if err := tx.Bucket(bucketCredentials).Delete(key); err != nil {
			return err
		}
		if credential.Prefix != "" {
			if err := tx.Bucket(bucketCredentialByPrefix).Delete(scopedKey(scope, credential.Prefix)); err != nil {
				return err
			}
		}
		if credential.HMACKeyID != "" && len(credential.SecretHMAC) > 0 {
			index := credential.HMACKeyID + "\x00" + hex.EncodeToString(credential.SecretHMAC)
			if err := tx.Bucket(bucketCredentialByHMAC).Delete(scopedKey(scope, index)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *BoltStore) TouchCredential(_ context.Context, scope Scope, id string, at time.Time) error {
	scope = scope.Normalize()
	return s.db.Update(func(tx *bolt.Tx) error {
		key := scopedKey(scope, id)
		var credential Credential
		if err := decode(tx.Bucket(bucketCredentials).Get(key), &credential); err != nil {
			return err
		}
		value := at.UTC()
		credential.LastUsedAt = &value
		return put(tx.Bucket(bucketCredentials), key, credential)
	})
}

func (s *BoltStore) GetPolicy(_ context.Context, scope Scope, id string) (ClientPolicy, error) {
	scope = scope.Normalize()
	var policy ClientPolicy
	err := s.db.View(func(tx *bolt.Tx) error {
		return decode(tx.Bucket(bucketPolicies).Get(scopedKey(scope, id)), &policy)
	})
	if err == nil && policy.ProjectID != scope.ProjectID {
		err = ErrNotFound
	}
	return policy, err
}

func (s *BoltStore) UpdatePolicy(_ context.Context, scope Scope, policy ClientPolicy) error {
	scope = scope.Normalize()
	policy.ProjectID = scope.ProjectID
	if err := policy.Validate(); err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		key := scopedKey(scope, policy.ID)
		if tx.Bucket(bucketPolicies).Get(key) == nil {
			return ErrNotFound
		}
		return put(tx.Bucket(bucketPolicies), key, policy)
	})
}

func (s *BoltStore) RequiredHMACKeyIDs(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketCredentials).ForEach(func(_, raw []byte) error {
			var credential Credential
			if err := decode(raw, &credential); err != nil {
				return err
			}
			if credential.HMACKeyID != "" && credential.Status != CredentialRevoked {
				ids[credential.HMACKeyID] = true
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}
