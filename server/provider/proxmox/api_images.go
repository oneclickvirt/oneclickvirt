package proxmox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"oneclickvirt/provider"
)

// apiListImages 通过API方式获取Proxmox镜像列表
func (p *ProxmoxProvider) apiListImages(ctx context.Context) ([]provider.Image, error) {
	url := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/storage/local/content", p.config.Host, p.node)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	// 设置认证头
	p.setAPIAuth(req)

	resp, err := p.apiClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var response map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}

	var images []provider.Image
	if data, ok := response["data"].([]interface{}); ok {
		for _, item := range data {
			if imageData, ok := item.(map[string]interface{}); ok {
				if imageData["content"].(string) == "iso" {
					image := provider.Image{
						ID:   imageData["volid"].(string),
						Name: imageData["volid"].(string),
						Tag:  "iso",
						Size: fmt.Sprintf("%.2f MB", imageData["size"].(float64)/1024/1024),
					}
					images = append(images, image)
				}
			}
		}
	}

	return images, nil
}

// apiPullImage 通过API方式拉取Proxmox镜像
func (p *ProxmoxProvider) apiPullImage(ctx context.Context, image string) error {
	// Proxmox API 拉取镜像与SSH方式一致，都是直接下载文件到文件系统
	// 因为Proxmox没有独立的镜像仓库API，所以使用SSH方式下载
	return p.sshPullImage(ctx, image)
}

// apiDeleteImage 通过API方式删除Proxmox镜像
func (p *ProxmoxProvider) apiDeleteImage(ctx context.Context, id string) error {
	url := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/storage/local/content/%s", p.config.Host, p.node, id)
	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return err
	}

	// 设置认证头
	p.setAPIAuth(req)

	resp, err := p.apiClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to delete image: %d", resp.StatusCode)
	}

	return nil
}
