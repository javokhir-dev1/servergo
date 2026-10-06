//go:build linux && (amd64 || arm64)

package sandbox

import (
	"bytes"
	"encoding/binary"
	"runtime"

	"golang.org/x/sys/unix"
)

// Seccomp — sandbox ichidagi ilovaga ruxsat etilmagan tizim chaqiruvlari.
//
// Mount namespace ilovaning fayllarini yopadi, PID namespace qo'shnilarini
// yashiradi — lekin ikkalasi ham kernel'ning o'zidagi teshikdan qochib
// chiqishga qarshi himoya qilmaydi. Bu yerdagi ro'yxat aynan shu yo'llarni
// kesadi: ptrace va process_vm_* (boshqa jarayonning xotirasi), keyctl
// (kernel kalit halqasi), bpf/perf_event_open/io_uring (ekspluatatsiya uchun
// mashhur sirtlar), modul yuklash, mount/pivot_root/setns/unshare (yangi
// namespace ochib himoyani aylanib o'tish).
//
// Ro'yxatdagi chaqiruv "yo'q" (ENOSYS) deb javob beradi, "ruxsat yo'q"
// (EPERM) emas — kutubxonalar ENOSYS'ni "bu kernelda yo'q" deb tushunib,
// o'zi muqobil yo'lga o'tadi (masalan libuv io_uring'dan threadpool'ga).
// Shu tufayli oddiy ilovalar hech narsani sezmaydi.
var deniedSyscalls = []int{
	unix.SYS_PTRACE,
	unix.SYS_PROCESS_VM_READV,
	unix.SYS_PROCESS_VM_WRITEV,
	unix.SYS_KEYCTL,
	unix.SYS_ADD_KEY,
	unix.SYS_REQUEST_KEY,
	unix.SYS_BPF,
	unix.SYS_PERF_EVENT_OPEN,
	unix.SYS_USERFAULTFD,
	unix.SYS_KEXEC_LOAD,
	unix.SYS_KEXEC_FILE_LOAD,
	unix.SYS_INIT_MODULE,
	unix.SYS_FINIT_MODULE,
	unix.SYS_DELETE_MODULE,
	unix.SYS_OPEN_BY_HANDLE_AT,
	unix.SYS_NAME_TO_HANDLE_AT,
	unix.SYS_PIVOT_ROOT,
	unix.SYS_MOUNT,
	unix.SYS_UMOUNT2,
	unix.SYS_MOVE_MOUNT,
	unix.SYS_FSOPEN,
	unix.SYS_FSCONFIG,
	unix.SYS_FSMOUNT,
	unix.SYS_OPEN_TREE,
	unix.SYS_MOUNT_SETATTR,
	unix.SYS_SETNS,
	unix.SYS_UNSHARE,
	unix.SYS_IO_URING_SETUP,
	unix.SYS_QUOTACTL,
	unix.SYS_SWAPON,
	unix.SYS_SWAPOFF,
	unix.SYS_ACCT,
	unix.SYS_SYSLOG,
}

// cBPF komandalar va seccomp javoblari (linux/filter.h, linux/seccomp.h).
const (
	bpfLDAbsW = 0x20 // BPF_LD | BPF_W | BPF_ABS — seccomp_data dan 4 bayt o'qish
	bpfJEQ    = 0x15 // BPF_JMP | BPF_JEQ | BPF_K
	bpfRET    = 0x06 // BPF_RET | BPF_K

	offNR   = 0 // seccomp_data.nr
	offArch = 4 // seccomp_data.arch

	retAllow        = 0x7fff0000      // SECCOMP_RET_ALLOW
	retErrnoENOSYS  = 0x00050000 | 38 // SECCOMP_RET_ERRNO | ENOSYS
	retKillProcess  = 0x80000000      // SECCOMP_RET_KILL_PROCESS
	auditArchX86_64 = 0xC000003E
	auditArchARM64  = 0xC00000B7
)

// sockFilter — struct sock_filter (linux/filter.h).
type sockFilter struct {
	Code uint16
	JT   uint8
	JF   uint8
	K    uint32
}

// SeccompProgram — bwrap'ning --seccomp bayrog'iga beriladigan cBPF dasturi.
// Qaytgan baytlar sock_filter massivi: bwrap ularni o'zgarishsiz kernel'ga
// uzatadi.
func SeccompProgram() []byte {
	arch := uint32(auditArchX86_64)
	if runtime.GOARCH == "arm64" {
		arch = auditArchARM64
	}

	n := len(deniedSyscalls)
	// Tuzilishi:
	//   0        arch ni o'qish
	//   1        arch mos kelmasa -> KILL (32-bitli chaqiruv orqali ro'yxatni
	//            aylanib o'tishning oldini oladi)
	//   2        nr ni o'qish
	//   3..3+n-1 har bir taqiqlangan nr uchun solishtirish -> ENOSYS
	//   3+n      ALLOW
	//   4+n      ENOSYS
	//   5+n      KILL
	prog := make([]sockFilter, 0, n+6)
	prog = append(prog,
		sockFilter{Code: bpfLDAbsW, K: offArch},
		// jf — keyingi emas, KILL ga sakrash (nisbiy masofa).
		sockFilter{Code: bpfJEQ, JT: 0, JF: uint8(3 + n), K: arch},
		sockFilter{Code: bpfLDAbsW, K: offNR},
	)
	for i, nr := range deniedSyscalls {
		idx := 3 + i
		prog = append(prog, sockFilter{
			Code: bpfJEQ,
			JT:   uint8(4 + n - idx - 1), // ENOSYS qatoriga
			JF:   0,                      // mos kelmasa — keyingi solishtirish
			K:    uint32(nr),
		})
	}
	prog = append(prog,
		sockFilter{Code: bpfRET, K: retAllow},
		sockFilter{Code: bpfRET, K: retErrnoENOSYS},
		sockFilter{Code: bpfRET, K: retKillProcess},
	)

	var buf bytes.Buffer
	for _, ins := range prog {
		_ = binary.Write(&buf, binary.LittleEndian, ins)
	}
	return buf.Bytes()
}
