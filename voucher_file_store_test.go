// SPDX-FileCopyrightText: (C) 2026 Dell Technologies
// SPDX-License-Identifier: Apache 2.0

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBackupAndRestoreVoucher(t *testing.T) {
	dir := t.TempDir()
	store := NewVoucherFileStore(dir)

	guid := "aabbccdd11223344"
	path := store.FilePathForGUID(guid)

	// Write a fake "original" voucher file
	original := []byte("original-voucher-content")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}

	// Backup
	if err := store.BackupVoucher(guid); err != nil {
		t.Fatalf("BackupVoucher failed: %v", err)
	}

	// Verify backup file exists
	backupPath := path + ".preassign"
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("backup file not created: %v", err)
	}

	// Overwrite the voucher file (simulating ExtendVoucher + SaveVoucher)
	extended := []byte("extended-voucher-content")
	if err := os.WriteFile(path, extended, 0o644); err != nil {
		t.Fatal(err)
	}

	// Verify the current file is the extended version
	data, _ := os.ReadFile(path)
	if string(data) != "extended-voucher-content" {
		t.Fatalf("expected extended content, got %q", string(data))
	}

	// Restore
	if err := store.RestoreVoucher(guid); err != nil {
		t.Fatalf("RestoreVoucher failed: %v", err)
	}

	// Verify restored content matches original
	data, _ = os.ReadFile(path)
	if string(data) != "original-voucher-content" {
		t.Errorf("restored content = %q, want %q", string(data), "original-voucher-content")
	}

	// Verify backup file was cleaned up
	if _, err := os.Stat(backupPath); !os.IsNotExist(err) {
		t.Error("backup file should be removed after restore")
	}
}

func TestRestoreVoucher_NoBackup(t *testing.T) {
	dir := t.TempDir()
	store := NewVoucherFileStore(dir)

	guid := "deadbeef12345678"
	path := store.FilePathForGUID(guid)

	// Write a voucher file but no backup
	if err := os.WriteFile(path, []byte("some-content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// RestoreVoucher should fail when no backup exists
	err := store.RestoreVoucher(guid)
	if err == nil {
		t.Fatal("expected error when restoring without backup")
	}
}

func TestBackupVoucher_NoFile(t *testing.T) {
	dir := t.TempDir()
	store := NewVoucherFileStore(dir)

	// BackupVoucher should fail when the original file doesn't exist
	err := store.BackupVoucher("nonexistent-guid-1234")
	if err == nil {
		t.Fatal("expected error when backing up nonexistent file")
	}
}

func TestFilePathForGUID(t *testing.T) {
	store := NewVoucherFileStore("/vouchers")

	got := store.FilePathForGUID("aabbccdd")
	want := filepath.Join("/vouchers", "aabbccdd.fdoov")
	if got != want {
		t.Errorf("FilePathForGUID = %q, want %q", got, want)
	}

	// Empty GUID should return empty
	if got := store.FilePathForGUID(""); got != "" {
		t.Errorf("FilePathForGUID empty = %q, want empty", got)
	}

	// Nil store should return empty
	var nilStore *VoucherFileStore
	if got := nilStore.FilePathForGUID("abc"); got != "" {
		t.Errorf("nil store FilePathForGUID = %q, want empty", got)
	}
}

func TestBackupVoucher_Idempotent(t *testing.T) {
	dir := t.TempDir()
	store := NewVoucherFileStore(dir)

	guid := "idempotent-test1234"
	path := store.FilePathForGUID(guid)

	// Write original content
	original := []byte("original-content")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}

	// First backup
	if err := store.BackupVoucher(guid); err != nil {
		t.Fatal(err)
	}

	// Overwrite voucher (simulating assignment)
	if err := os.WriteFile(path, []byte("extended-content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Second backup should overwrite (this is the behavior we want — latest state before re-assign)
	if err := store.BackupVoucher(guid); err != nil {
		t.Fatal(err)
	}

	// Restore should get the second backup (extended content, not original)
	if err := store.RestoreVoucher(guid); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "extended-content" {
		t.Errorf("expected extended-content after second backup+restore, got %q", string(data))
	}
}
