package main

import (
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// dropPrivileges switches from root to the user in PUID and PGID, after the
// ICMP sockets and the web port are open; those keep working. The data
// folder is handed to that user first, so a folder Docker created as root
// becomes writable. Without PUID and PGID, or when not started as root,
// nothing changes. The Docker image sets PUID=1000 and PGID=1000.
func dropPrivileges(dataDir string) {
	if os.Geteuid() != 0 {
		return
	}
	uid, okU := envID("PUID")
	gid, okG := envID("PGID")
	if !okU && !okG {
		return
	}
	if !okG {
		gid = uid
	}
	if !okU {
		uid = gid
	}
	if uid == 0 {
		return
	}
	if n, err := chownTree(dataDir, uid, gid); err != nil {
		log.Printf("could not give %s to user %d: %v", dataDir, uid, err)
	} else if n > 0 {
		log.Printf("gave %d files in %s to user %d:%d", n, dataDir, uid, gid)
	}
	if err := syscall.Setgroups([]int{}); err != nil {
		log.Printf("keeping root: cannot change groups (%v); the container may lack the SETGID capability", err)
		return
	}
	if err := syscall.Setgid(gid); err != nil {
		log.Printf("keeping root: cannot switch to group %d (%v); the container may lack the SETGID capability", gid, err)
		return
	}
	if err := syscall.Setuid(uid); err != nil {
		log.Printf("keeping root: cannot switch to user %d (%v); the container may lack the SETUID capability", uid, err)
		return
	}
	log.Printf("running as user %d:%d", uid, gid)
}

func envID(key string) (int, bool) {
	v := os.Getenv(key)
	if v == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		log.Printf("ignoring %s=%q: not a number", key, v)
		return 0, false
	}
	return n, true
}

// chownTree gives every file under dir that is not owned by uid:gid to it.
func chownTree(dir string, uid, gid int) (int, error) {
	changed := 0
	var first error
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if first == nil {
				first = err
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) == uid && int(st.Gid) == gid {
			return nil
		}
		if err := os.Lchown(p, uid, gid); err != nil {
			if first == nil {
				first = err
			}
			return nil
		}
		changed++
		return nil
	})
	if first == nil {
		first = err
	}
	return changed, first
}
