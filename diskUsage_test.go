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

// sdCard1 mimics sdCardDisks() on a board whose mmcblk1 is the card slot and
// whose mmcblk0 is the soldered eMMC.
var sdCard1 = map[string]bool{"/dev/mmcblk1": true}

func TestPickExtraDiskMounts(t *testing.T) {
	nvme, sd := pickExtraDiskMounts(parseBlockMounts(procMounts), sdCard1)
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
	nvme, sd := pickExtraDiskMounts(mounts, sdCard1)
	if nvme != "" || sd != "" {
		t.Errorf("got nvme=%q sd=%q, want both empty", nvme, sd)
	}
}

func TestPickExtraDiskMountsRootOnNvme(t *testing.T) {
	// Booted from NVMe: the root NVMe disk must not be reported again, and the
	// eMMC is not a card so it must not take the SD slot either.
	mounts := parseBlockMounts(`/dev/nvme0n1p2 / ext4 rw 0 0
/dev/mmcblk0p7 /mnt/emmc ext4 rw 0 0
`)
	nvme, sd := pickExtraDiskMounts(mounts, sdCard1)
	if nvme != "" {
		t.Errorf("nvme mountpoint = %q, want empty (root disk)", nvme)
	}
	if sd != "" {
		t.Errorf("sd mountpoint = %q, want empty (mmcblk0 is eMMC, not a card)", sd)
	}
}

// On OpenWrt the root filesystem is an overlay, so no mount line names the root
// disk: the eMMC /boot partition must still not be taken for an SD card.
func TestPickExtraDiskMountsOverlayRootIgnoresEmmcBoot(t *testing.T) {
	mounts := parseBlockMounts(`/dev/root /rom squashfs ro 0 0
/dev/loop0 /overlay f2fs rw 0 0
overlayfs:/overlay / overlay rw 0 0
/dev/nvme0n1p1 /mnt/nvme0n1p1 ext4 rw 0 0
/dev/mmcblk0p1 /boot ext4 ro 0 0
`)
	nvme, sd := pickExtraDiskMounts(mounts, sdCard1)
	if nvme != "/mnt/nvme0n1p1" {
		t.Errorf("nvme mountpoint = %q, want /mnt/nvme0n1p1", nvme)
	}
	if sd != "" {
		t.Errorf("sd mountpoint = %q, want empty (/boot is on the eMMC)", sd)
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

// collectDiskUsage must publish percent + presence flags used by disk_bars
// (OpenWrt and Debian share the same keys).
func TestCollectDiskUsageStoresPercents(t *testing.T) {
	collectDiskUsage()
	if _, ok := globalData.Load("DiskUsagePercent"); !ok {
		t.Error("DiskUsagePercent missing after collectDiskUsage")
	}
	if _, ok := globalData.Load("DiskNvmePercent"); !ok {
		t.Error("DiskNvmePercent missing after collectDiskUsage")
	}
	if _, ok := globalData.Load("DiskNvmePresent"); !ok {
		t.Error("DiskNvmePresent missing after collectDiskUsage")
	}
	if _, ok := globalData.Load("DiskSDPercent"); !ok {
		t.Error("DiskSDPercent missing after collectDiskUsage")
	}
	if _, ok := globalData.Load("DiskSDPresent"); !ok {
		t.Error("DiskSDPresent missing after collectDiskUsage")
	}
	if v, ok := globalData.Load("DiskUsagePercent"); ok {
		pct, ok := v.(float64)
		if !ok || pct < 0 || pct > 100 {
			t.Errorf("DiskUsagePercent = %v, want float64 in [0,100]", v)
		}
	}
}
