//go:build !linux

// Sandbox faqat Linux'da (bubblewrap + user namespace) ishlaydi. Boshqa
// tizimlarda bo'lim ishlayveradi, lekin sandbox'ni yoqib bo'lmaydi.
package sandbox

import (
	"encoding/json"
	"errors"
	"io"
	"time"
)

const BinName = "bwrap"

type Spec struct {
	Cwd string
	RO  []string
	RW  []string
	Net *NetSpec
}

type NetSpec struct {
	HostPorts  []int
	ResolvConf string
}

const NetBinName = "pasta"

const NoFD = -1

func NetAvailable() error { return errUnsupported }

func EnsureResolvConf(dir string) (string, error) { return "", errUnsupported }

type Info struct {
	ChildPID int    `json:"child-pid"`
	PidNS    uint64 `json:"pid-namespace"`
}

var errUnsupported = errors.New("sandbox faqat Linux'da qo'llanadi")

func Available() error { return errUnsupported }

func Validate(sp Spec) error { return errUnsupported }

func Argv(sp Spec, command string, infoFD, seccompFD int) ([]string, error) {
	return nil, errUnsupported
}

func ParseInfo(r io.Reader) (Info, error) {
	var in Info
	if err := json.NewDecoder(r).Decode(&in); err != nil {
		return in, err
	}
	return in, nil
}

func ProcsInNS(inode uint64) []int { return nil }

func FindNS(rootPID int, wait time.Duration) (Info, error) { return Info{}, errUnsupported }
