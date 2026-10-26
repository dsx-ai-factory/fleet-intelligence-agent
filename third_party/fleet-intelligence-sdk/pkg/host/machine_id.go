// Copyright 2024 Lepton AI Inc
// Source: https://github.com/leptonai/gpud

package host

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"

	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/file"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
	"github.com/google/uuid"
)

// Returns the UUID of the machine host.
// Returns an empty string if the UUID is not found.
func GetMachineID(ctx context.Context) (string, error) {
	// hw-based UUID first
	uuid, err := GetDmidecodeUUID(ctx)
	if err != nil {
		log.Logger.Warnw("failed to get UUID from dmidecode, trying to read from file", "error", err)

		// otherwise, try to read from file
		return GetOSMachineID()
	}
	return uuid, nil
}

// Fetches the UUIF of the machine host, using the "dmidecode".
// Returns an empty string if the UUID is not found.
//
// ref.
// UUID=$(dmidecode -t 1 | grep -i UUID | awk '{print $2}')
func GetDmidecodeUUID(ctx context.Context) (string, error) {
	dmidecodePath, err := file.LocateExecutable("dmidecode")
	if err != nil {
		return "", errors.New("dmidecode not found")
	}

	output, err := exec.CommandContext(ctx, dmidecodePath, "-t", "system").CombinedOutput()
	combinedOutput := strings.TrimSpace(string(output))
	if err != nil {
		return "", fmt.Errorf("failed to read dmidecode for uuid: %w\n\noutput:\n%s", err, combinedOutput)
	}

	dmidecodeUUID, err := parseDmidecodeUUIDFromOutput(combinedOutput)
	if err != nil {
		return "", err
	}

	return dmidecodeUUID, nil
}

func parseDmidecodeUUIDFromOutput(output string) (string, error) {
	dmidecodeUUID := ""
	for _, line := range strings.Split(output, "\n") {
		u := extractUUID(line)
		if u != "" {
			dmidecodeUUID = u
		}
	}
	if err := validateDmidecodeUUID(dmidecodeUUID); err != nil {
		return "", err
	}
	return dmidecodeUUID, nil
}

func extractUUID(line string) string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "UUID: ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "UUID: "))
}

func validateDmidecodeUUID(id string) error {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return errors.New("dmidecode UUID not found")
	}
	if _, err := uuid.Parse(trimmed); err != nil {
		return fmt.Errorf("invalid UUID from dmidecode %q: %w", trimmed, err)
	}
	return nil
}

// ref. https://github.com/google/cadvisor/blob/854445c010e0b634fcd855a20681ae986da235df/machine/info.go#L39
var machineIDPaths = []string{
	"/etc/machine-id",
	"/var/lib/dbus/machine-id",
}

// GetOSMachineID returns the OS-level UUID based on /etc/machine-id or /var/lib/dbus/machine-id.
// Returns an empty string if the UUID is not found.
func GetOSMachineID() (string, error) {
	return getOSMachineID(machineIDPaths)
}

func getOSMachineID(files []string) (string, error) {
	for _, path := range files {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(content)), nil
	}
	return "", nil
}

// GetOSName reads the os name from the /etc/os-release file.
func GetOSName() (string, error) {
	return getOSName("/etc/os-release")
}

func getOSName(file string) (string, error) {
	if _, err := os.Stat(file); err != nil {
		return "", err
	}

	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = f.Close()
	}()

	name := ""
	prettyName := ""
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "NAME=") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "NAME="))
			name = strings.TrimSpace(strings.Trim(name, "\""))
		}
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			prettyName = strings.TrimSpace(strings.TrimPrefix(line, "PRETTY_NAME="))
			prettyName = strings.TrimSpace(strings.Trim(prettyName, "\""))
		}
	}
	if prettyName != "" {
		return prettyName, nil
	}
	return name, nil
}

const dmiDir = "/sys/class/dmi"

// GetSystemUUID returns the DMI product UUID of the machine.
// It returns an empty string and an error when the UUID is missing, invalid, or
// an all-zero/all-f placeholder. It never falls back to another identifier such
// as /etc/machine-id, because the backend uses this value as hardware identity
// evidence.
func GetSystemUUID() (string, error) {
	return getSystemUUID(path.Join(dmiDir, "id", "product_uuid"))
}

func getSystemUUID(productUUIDPath string) (string, error) {
	content, err := os.ReadFile(productUUIDPath)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(content))
	parsed, err := uuid.Parse(id)
	if err != nil || len(id) != 36 {
		return "", fmt.Errorf("invalid DMI product UUID %q", id)
	}
	if parsed == uuid.Nil || parsed == uuid.Max {
		return "", fmt.Errorf("DMI product UUID %q is a placeholder", id)
	}
	return id, nil
}
