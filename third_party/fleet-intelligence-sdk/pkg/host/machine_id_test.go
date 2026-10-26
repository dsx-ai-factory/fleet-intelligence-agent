// Copyright 2024 Lepton AI Inc
// Source: https://github.com/leptonai/gpud

package host

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestScanUUIDFromDmidecode(t *testing.T) {
	f, err := os.Open("testdata/dmidecode")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = f.Close()
	}()

	scanner := bufio.NewScanner(f)
	uuid := ""
	for scanner.Scan() {
		uuid = extractUUID(scanner.Text())
		if uuid != "" {
			break
		}
	}
	if uuid != "4c4c4544-0053-5210-8038-c8c04f583034" {
		t.Errorf("expected UUID to be 4c4c4544-0053-5210-8038-c8c04f583034, got %s", uuid)
	}
}

func TestValidateDmidecodeUUID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{
			name:    "valid_uuid",
			id:      "4c4c4544-0053-5210-8038-c8c04f583034",
			wantErr: false,
		},
		{
			name:    "placeholder_value",
			id:      "Not Settable",
			wantErr: true,
		},
		{
			name:    "empty_value",
			id:      "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDmidecodeUUID(tt.id)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateDmidecodeUUID() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestParseDmidecodeUUIDFromOutput(t *testing.T) {
	output := `# dmidecode 3.5
System Information
	Manufacturer: Dell Inc.
	UUID: 4c4c4544-0047-4210-8035-c8c04f583034
`

	id, err := parseDmidecodeUUIDFromOutput(output)
	if err != nil {
		t.Fatalf("parseDmidecodeUUIDFromOutput() error = %v", err)
	}
	if id != "4c4c4544-0047-4210-8035-c8c04f583034" {
		t.Fatalf("parseDmidecodeUUIDFromOutput() = %q, want valid UUID", id)
	}
}

func TestGetOSMachineID(t *testing.T) {
	// Create a temporary directory for test files
	tempDir, err := os.MkdirTemp("", "machine_id_test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	// Create test files
	firstFilePath := filepath.Join(tempDir, "first_id")
	secondFilePath := filepath.Join(tempDir, "second_id")
	noPermFilePath := filepath.Join(tempDir, "no_perm_id")

	// Write content to test files
	if err := os.WriteFile(firstFilePath, []byte("machine-id-123\n"), 0644); err != nil {
		t.Fatalf("Failed to write to test file: %v", err)
	}
	if err := os.WriteFile(secondFilePath, []byte("  machine-id-456  "), 0644); err != nil {
		t.Fatalf("Failed to write to test file: %v", err)
	}
	if err := os.WriteFile(noPermFilePath, []byte("no-perm-id"), 0000); err != nil {
		t.Fatalf("Failed to write to test file: %v", err)
	}

	nonExistentPath := filepath.Join(tempDir, "does_not_exist")

	tests := []struct {
		name     string
		files    []string
		expected string
		wantErr  bool
	}{
		{
			name:     "First file exists",
			files:    []string{firstFilePath, secondFilePath},
			expected: "machine-id-123",
			wantErr:  false,
		},
		{
			name:     "Skip non-existent file",
			files:    []string{nonExistentPath, secondFilePath},
			expected: "machine-id-456",
			wantErr:  false,
		},
		{
			name:     "No files exist",
			files:    []string{nonExistentPath, nonExistentPath + "2"},
			expected: "",
			wantErr:  false,
		},
		{
			name:     "Permission denied",
			files:    []string{noPermFilePath},
			expected: "",
			wantErr:  true,
		},
		{
			name:     "Empty file list",
			files:    []string{},
			expected: "",
			wantErr:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := getOSMachineID(tc.files)

			if (err != nil) != tc.wantErr {
				t.Errorf("getOSMachineID() error = %v, wantErr %v", err, tc.wantErr)
				return
			}

			if got != tc.expected {
				t.Errorf("getOSMachineID() = %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestGetOSMachineIDWithTestData(t *testing.T) {
	// Test using the sample file in testdata directory
	files := []string{"testdata/machine_id_sample"}

	id, err := getOSMachineID(files)
	if err != nil {
		t.Fatalf("Failed to get machine ID from test file: %v", err)
	}

	expected := "sample-machine-id-12345"
	if id != expected {
		t.Errorf("getOSMachineID() = %q, want %q", id, expected)
	}
}

func TestGetSystemUUID(t *testing.T) {
	tmpDir := t.TempDir()
	testCases := []struct {
		name     string
		content  *string
		expected string
		wantErr  bool
	}{
		{name: "valid UUID", content: ptr("4c4c4544-004a-3410-804c-c7c04f313434\n"), expected: "4c4c4544-004a-3410-804c-c7c04f313434"},
		{name: "uppercase UUID is preserved", content: ptr("  4C4C4544-004A-3410-804C-C7C04F313434  "), expected: "4C4C4544-004A-3410-804C-C7C04F313434"},
		{name: "missing file", wantErr: true},
		{name: "empty", content: ptr("\n"), wantErr: true},
		{name: "not a UUID", content: ptr("Not Settable"), wantErr: true},
		{name: "machine-id form", content: ptr("4c4c4544004a3410804cc7c04f313434"), wantErr: true},
		{name: "all-zero placeholder is reported", content: ptr("00000000-0000-0000-0000-000000000000"), expected: "00000000-0000-0000-0000-000000000000"},
		{name: "all-f placeholder is reported", content: ptr("FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF"), expected: "FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF"},
	}

	for i, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			productUUIDPath := filepath.Join(tmpDir, fmt.Sprintf("product_uuid_%d", i))
			if tc.content != nil {
				if err := os.WriteFile(productUUIDPath, []byte(*tc.content), 0o644); err != nil {
					t.Fatalf("Failed to write test file: %v", err)
				}
			}

			got, err := getSystemUUID(productUUIDPath)
			if (err != nil) != tc.wantErr {
				t.Fatalf("getSystemUUID() error = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.expected {
				t.Errorf("getSystemUUID() = %q, want %q", got, tc.expected)
			}
		})
	}
}

func ptr(s string) *string {
	return &s
}

func Test_getOSName(t *testing.T) {
	name, err := getOSName("testdata/etc-os-release")
	if err != nil {
		t.Fatalf("Failed to get OS name: %v", err)
	}
	if name != "Ubuntu 22.04.5 LTS" {
		t.Errorf("getOSName() = %q, want %q", name, "Ubuntu 22.04.5 LTS")
	}
}
