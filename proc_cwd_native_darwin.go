//go:build darwin && cgo

package terminal

/*
#cgo LDFLAGS: -lproc
#include <libproc.h>
#include <sys/proc_info.h>
#include <string.h>
static int dw_process_cwd(int pid, char *path, int size) {
 struct proc_vnodepathinfo info;
 int n = proc_pidinfo(pid, PROC_PIDVNODEPATHINFO, 0, &info, sizeof(info));
 if (n != sizeof(info)) return 0;
 strlcpy(path, info.pvi_cdir.vip_path, size);
 return path[0] != 0;
}
*/
import "C"

// libproc reads the kernel's cwd directly, without spawning lsof or touching
// every mount. Non-cgo distributions retain the existing lsof fallback.
func nativeProcessCWD(pid int) string {
	var path [4096]C.char
	if C.dw_process_cwd(C.int(pid), &path[0], C.int(len(path))) == 0 {
		return ""
	}
	return C.GoString(&path[0])
}
