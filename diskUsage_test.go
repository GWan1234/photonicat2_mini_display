package main

import "testing"

// procMounts mimics a pcat2 Debian /proc/mounts with root on eMMC, an NVMe
// drive, an SD card, and the usual pseudo filesystems that must be ignored.
const procMounts = `sysfs /sys sysfs rw,nosuid,nodev,noexec,relatime 0 0
proc /proc proc rw,nosuid,nodev,noexec,relatime 0 0
udev /dev devtmpfs rw,nosuid,relatime,size=1873464k 0 0
/dev/mmcblk0p7 / ext4 rw,relatime 0 0
/dev/mmcblk0p6 /boot ext4 rw,relatime 0 0
tmpfs /run tmpfs rw,nosuid,nodev 0 0
/dev/nvme0n1p1 /mnt/nvme ext4 rw,relatime 0 0
/dev/nvme0n1p2 /mnt/nvme2 ext4 rw,relatime 0 0
/dev/mmcblk1p1 /mnt/sdcard vfat rw,relatime 0 0
overlay /var/lib/docker/overlay2/abc/merged overlay rw,relatime 0 0
`

func TestPickExtraDiskMounts(t *testing.T) {
	nvme, sd := pickExtraDiskMounts(parseBlockMounts(procMounts))
	if nvme != "/mnt/nvme" {
		t.Errorf("nvme mountpoint = %q, want /mnt/nvme", nvme)
	}
	if sd != "/mnt/sdcard" {
		t.Errorf("sd mountpoint = %q, want /mnt/sdcard", sd)
	}
}

func TestPickExtraDiskMountsIgnoresRootDisk(t *testing.T) {
	// Only root-disk partitions and pseudo mounts: nothing extra to report.
	mounts := parseBlockMounts(`/dev/mmcblk0p7 / ext4 rw 0 0
/dev/mmcblk0p6 /boot ext4 rw 0 0
tmpfs /run tmpfs rw 0 0
`)
	nvme, sd := pickExtraDiskMounts(mounts)
	if nvme != "" || sd != "" {
		t.Errorf("got nvme=%q sd=%q, want both empty", nvme, sd)
	}
}

func TestPickExtraDiskMountsRootOnNvme(t *testing.T) {
	// Booted from NVMe: the eMMC then shows up in the SD slot, and the root
	// NVMe disk itself must not be reported again.
	mounts := parseBlockMounts(`/dev/nvme0n1p2 / ext4 rw 0 0
/dev/mmcblk0p7 /mnt/emmc ext4 rw 0 0
`)
	nvme, sd := pickExtraDiskMounts(mounts)
	if nvme != "" {
		t.Errorf("nvme mountpoint = %q, want empty (root disk)", nvme)
	}
	if sd != "/mnt/emmc" {
		t.Errorf("sd mountpoint = %q, want /mnt/emmc", sd)
	}
}

func TestDiskBaseRegex(t *testing.T) {
	cases := map[string]string{
		"/dev/mmcblk0p7":  "/dev/mmcblk0",
		"/dev/mmcblk1p1":  "/dev/mmcblk1",
		"/dev/nvme0n1p1":  "/dev/nvme0n1",
		"/dev/sda1":       "/dev/sda",
		"/dev/mapper/vg0": "",
		"/dev/loop3":      "",
	}
	for dev, want := range cases {
		if got := reDiskBase.FindString(dev); got != want {
			t.Errorf("reDiskBase(%q) = %q, want %q", dev, got, want)
		}
	}
}
