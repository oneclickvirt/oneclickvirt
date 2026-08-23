package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"debug/elf"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	maxServerArchiveSize = 512 << 20
	maxWebArchiveSize    = 512 << 20
	maxArchiveFiles      = 10000
)

func extractServerArchive(archivePath, destination string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return err
	}
	if stat.Size() <= 0 || stat.Size() > maxServerArchiveSize {
		return fmt.Errorf("主控发布包大小异常")
	}
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("主控发布包不是有效 gzip: %w", err)
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(io.LimitReader(gzipReader, maxServerArchiveSize))
	files := 0
	foundServer := false
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("读取主控发布包失败: %w", err)
		}
		files++
		if files > maxArchiveFiles {
			return fmt.Errorf("主控发布包包含过多文件")
		}
		name, err := safeArchivePath(header.Name)
		if err != nil {
			return err
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return fmt.Errorf("主控发布包包含不支持的文件类型")
		}
		if header.Size <= 0 || header.Size > maxServerArchiveSize {
			return fmt.Errorf("主控二进制大小异常")
		}
		if !looksLikeServerBinary(name) {
			continue
		}
		if foundServer {
			return fmt.Errorf("主控发布包包含多个服务端二进制")
		}
		target := filepath.Join(destination, "server")
		if err := writeLimitedFile(target, tarReader, header.Size, 0755); err != nil {
			return err
		}
		foundServer = true
	}
	if !fileExists(filepath.Join(destination, "server")) {
		return fmt.Errorf("主控发布包中未找到 Linux 服务端二进制")
	}
	return validateServerBinary(filepath.Join(destination, "server"))
}

func extractWebArchive(archivePath, destination string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("Web 发布包不是有效 zip: %w", err)
	}
	defer reader.Close()
	files := 0
	var total int64
	for _, entry := range reader.File {
		files++
		if files > maxArchiveFiles {
			return fmt.Errorf("Web 发布包包含过多文件")
		}
		name, err := safeArchivePath(entry.Name)
		if err != nil {
			return err
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		if entry.UncompressedSize64 > maxWebArchiveSize || total+int64(entry.UncompressedSize64) > maxWebArchiveSize {
			return fmt.Errorf("Web 发布包大小异常")
		}
		// Zip symlinks can escape the staging directory after extraction.
		if entry.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Web 发布包包含不安全的符号链接")
		}
		if !entry.Mode().IsRegular() {
			return fmt.Errorf("Web 发布包包含不支持的文件类型")
		}
		input, err := entry.Open()
		if err != nil {
			return err
		}
		target := filepath.Join(destination, name)
		if err := os.MkdirAll(filepath.Dir(target), 0750); err != nil {
			input.Close()
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
		if err != nil {
			input.Close()
			return err
		}
		written, copyErr := io.CopyN(output, input, int64(entry.UncompressedSize64))
		closeErr := output.Close()
		input.Close()
		if copyErr != nil && copyErr != io.EOF {
			return copyErr
		}
		if closeErr != nil || written != int64(entry.UncompressedSize64) {
			return fmt.Errorf("写入 Web 发布文件失败")
		}
		total += written
	}
	if !fileExists(filepath.Join(destination, "index.html")) {
		return fmt.Errorf("Web 发布包中未找到 index.html")
	}
	return nil
}

func safeArchivePath(name string) (string, error) {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	name = strings.TrimPrefix(name, "./")
	if name == "" || strings.HasPrefix(name, "/") || strings.ContainsRune(name, '\x00') {
		return "", fmt.Errorf("发布包包含非法路径")
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || filepath.IsAbs(clean) {
		return "", fmt.Errorf("发布包路径越界")
	}
	return clean, nil
}

func looksLikeServerBinary(name string) bool {
	base := filepath.Base(name)
	return strings.HasPrefix(base, "server-linux-") || strings.HasPrefix(base, "server-allinone-linux-") || base == "oneclickvirt-server"
}

func writeLimitedFile(path string, source io.Reader, size int64, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	written, copyErr := io.CopyN(file, source, size)
	closeErr := file.Close()
	if copyErr != nil && copyErr != io.EOF {
		return copyErr
	}
	if closeErr != nil || written != size {
		return fmt.Errorf("写入发布资产失败")
	}
	return nil
}

func validateServerBinary(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return err
	}
	if stat.Size() < 4 || stat.Size() > maxServerArchiveSize {
		return fmt.Errorf("服务端二进制大小异常")
	}
	if stat.Mode()&0111 == 0 {
		return fmt.Errorf("服务端二进制不可执行")
	}
	binary, err := elf.NewFile(file)
	if err != nil {
		return fmt.Errorf("服务端二进制不是有效 Linux ELF 文件: %w", err)
	}
	defer binary.Close()
	expected := expectedELFMachine()
	if expected == elf.EM_NONE || binary.Class != elf.ELFCLASS64 || binary.Machine != expected {
		return fmt.Errorf("服务端二进制架构与当前主机不匹配")
	}
	return nil
}

func expectedELFMachine() elf.Machine {
	switch runtime.GOARCH {
	case "amd64":
		return elf.EM_X86_64
	case "arm64":
		return elf.EM_AARCH64
	default:
		return elf.EM_NONE
	}
}
