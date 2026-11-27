//go:build !windows

package chatgptplan

// The credential file is created with mode 0600 on non-Windows systems.
func sealCredential(plain []byte) ([]byte, error)  { return plain, nil }
func unsealCredential(data []byte) ([]byte, error) { return data, nil }
