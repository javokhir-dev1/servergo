//go:build !linux || !(amd64 || arm64)

package sandbox

// SeccompProgram — bu arxitektura uchun filtr yozilmagan. nil qaytsa
// sandbox seccomp'siz ishlaydi (mount/PID izolyatsiyasi saqlanadi).
func SeccompProgram() []byte { return nil }
