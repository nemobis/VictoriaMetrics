package backupnames

import (
	"testing"
)

func TestConstantValues(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"RestoreInProgressFilename", RestoreInProgressFilename, "restore-in-progress"},
		{"RestoreMarkFileName", RestoreMarkFileName, "backup_restore.ignore"},
		{"ProtectMarkFileName", ProtectMarkFileName, "backup_locked.ignore"},
		{"BackupCompleteFilename", BackupCompleteFilename, "backup_complete.ignore"},
		{"BackupMetadataFilename", BackupMetadataFilename, "backup_metadata.ignore"},
	}
	for _, tc := range tests {
		if tc.value != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.value, tc.want)
		}
	}
}

func TestConstantsNonEmpty(t *testing.T) {
	constants := []string{
		RestoreInProgressFilename,
		RestoreMarkFileName,
		ProtectMarkFileName,
		BackupCompleteFilename,
		BackupMetadataFilename,
	}
	for _, c := range constants {
		if c == "" {
			t.Errorf("constant must not be empty")
		}
	}
}

func TestConstantsUnique(t *testing.T) {
	seen := make(map[string]bool)
	constants := map[string]string{
		"RestoreInProgressFilename": RestoreInProgressFilename,
		"RestoreMarkFileName":       RestoreMarkFileName,
		"ProtectMarkFileName":       ProtectMarkFileName,
		"BackupCompleteFilename":    BackupCompleteFilename,
		"BackupMetadataFilename":    BackupMetadataFilename,
	}
	for name, value := range constants {
		if seen[value] {
			t.Errorf("duplicate constant value %q for %s", value, name)
		}
		seen[value] = true
	}
}
