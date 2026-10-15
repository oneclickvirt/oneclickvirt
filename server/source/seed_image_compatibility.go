package source

import (
	"strings"

	"oneclickvirt/model/system"

	"gorm.io/gorm"
)

// Only exact URLs emitted by older built-in catalogues are migrated. Custom
// images, external URLs and other OS versions are never rewritten.
func seededImageURLReplacements() map[string]string {
	manual := "https://github.com/oneclickvirt/kvm_images/releases/download/"
	pve := "https://github.com/oneclickvirt/pve_kvm_images/releases/download/"
	replacements := map[string]string{
		"https://mirrors.tuna.tsinghua.edu.cn/osdn/android-x86/71931/android-x86_64-9.0-r2.iso": "https://sourceforge.net/projects/android-x86/files/Release%209.0/android-x86_64-9.0-r2.iso/download",
	}
	for name, target := range map[string]string{
		"debian11": "v2.0/debian11", "debian12": "v2.0/debian12",
		"ubuntu22": "v2.0/ubuntu22", "centos7": "v2.0/centos7",
		"opensuse-leap-15": "v1.0/opensuse-leap-15",
	} {
		replacements[manual+name+"/"+name+".qcow2"] = manual + target + ".qcow2"
	}
	for name, target := range map[string]string{
		"ubuntu24": "ubuntu/ubuntu2404", "almalinux9": "almalinux/almalinux9",
		"rockylinux9": "rockylinux/rockylinux9",
	} {
		replacements[manual+name+"/"+name+".qcow2"] = pve + target + ".qcow2"
	}
	for name, target := range map[string]string{
		"ubuntu18": "ubuntu/ubuntu1804", "ubuntu20": "ubuntu/ubuntu2004",
		"ubuntu22": "ubuntu/ubuntu2204", "ubuntu24": "ubuntu/ubuntu2404",
		"almalinux8": "almalinux/almalinux8", "almalinux9": "almalinux/almalinux9",
		"rockylinux8": "rockylinux/rockylinux8", "rockylinux9": "rockylinux/rockylinux9",
		"debian11": "debian/debian11", "debian12": "debian/debian12",
		"centos9-stream": "centos/centos9",
	} {
		replacements[pve+"images/"+name+".qcow2"] = pve + target + ".qcow2"
	}
	return replacements
}

func repairSeededImageURLs(db *gorm.DB, desired []system.SystemImage) error {
	replacements := seededImageURLReplacements()
	currentTemplates := map[string]system.SystemImage{}
	for _, image := range desired {
		if publishedImageBuildDate(image.URL) != "" {
			currentTemplates[SystemImageDedupKey(image)] = image
		}
	}
	broken := "https://github.com/oneclickvirt/kvm_images/releases/download/centos8-stream/centos8-stream.qcow2"
	return db.Transaction(func(tx *gorm.DB) error {
		var images []system.SystemImage
		if err := tx.Where("created_by IS NULL AND (url LIKE ? OR url = ?)", "https://github.com/oneclickvirt/%/releases/download/%", "https://mirrors.tuna.tsinghua.edu.cn/osdn/android-x86/71931/android-x86_64-9.0-r2.iso").Find(&images).Error; err != nil {
			return err
		}
		for _, image := range images {
			updates := map[string]interface{}{}
			if target, ok := currentTemplates[SystemImageDedupKey(image)]; ok &&
				publishedImageBuildDate(image.URL) != "" && publishedImageBuildDate(target.URL) > publishedImageBuildDate(image.URL) {
				replacements[image.URL] = target.URL
			}
			if target, ok := replacements[image.URL]; ok {
				updates["url"] = target
				// Checksums and sizes belong to the old asset.
				updates["checksum"], updates["size"] = "", 0
				parsed := parseImageURL(target)
				if parsed != nil && strings.EqualFold(image.OSType, parsed.OSType) {
					updates["os_version"] = parsed.OSVersion
				}
			} else if image.URL == broken && image.Status == "active" {
				// There is no corresponding manual CentOS Stream 8 release.
				updates["status"] = "inactive"
			}
			// Older parsers split x86_64 into x86 + 64 and treated some VM
			// archives as containers. Repair only the known invalid identities.
			if parsed := parseImageURL(image.URL); parsed != nil {
				if image.Architecture == "x86" && parsed.Architecture == "amd64" {
					updates["architecture"] = parsed.Architecture
					updates["instance_type"] = parsed.InstanceType
				}
				if strings.EqualFold(image.OSType, parsed.OSType) &&
					(strings.HasPrefix(image.OSVersion, parsed.OSVersion+".") ||
						(parsed.OSType == "ubuntu" && image.OSVersion != parsed.OSVersion &&
							strings.ReplaceAll(parsed.OSVersion, ".", "") == image.OSVersion) ||
						(parsed.OSType == "ubuntu" && parsed.OSVersion == image.OSVersion+".04")) {
					updates["os_version"] = parsed.OSVersion
				}
			}
			if len(updates) > 0 {
				if err := tx.Model(&system.SystemImage{}).Where("id = ? AND created_by IS NULL AND url = ? AND os_version = ? AND architecture = ? AND instance_type = ?", image.ID, image.URL, image.OSVersion, image.Architecture, image.InstanceType).Updates(updates).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}
