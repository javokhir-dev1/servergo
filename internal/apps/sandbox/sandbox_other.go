//go:build !linux

// Sandbox faqat Linux'da (bubblewrap + user namespace) ishlaydi. Boshqa
// tizimlarda bo'lim ishlayveradi, lekin sandbox'ni yoqib bo'lmaydi.
package sandbox

import (
	"encoding/json"
	"errors"
	"io"
)

const BinName = "bwrap"

type Spec struct {
	Cwd string
	RW  []string
}

type Info struct {
	ChildPID int    `json:"child-pid"`
	PidNS    uint64 `json:"pid-namespace"`
}

var errUnsupported = errors.New("sandbox faqat Linux'da qo'llanadi")

func Available() error { return errUnsupported }

func Validate(sp Spec) error { return errUnsupported }

func Argv(sp Spec, command string, infoFD int) ([]string, error) { return nil, errUnsupported }

func ParseInfo(r io.Reader) (Info, error) {
	var in Info
	if err := json.NewDecoder(r).Decode(&in); err != nil {
		return in, err
	}
	return in, nil
}

func ProcsInNS(inode uint64) []int { return nil }
