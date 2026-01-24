package activities

import (
	"context"
	"fmt"
	"strings"
)

type ImageInfo struct {
	Repository string
	Tag        string
}

func (a *WorkloadActivities) ParseImage(ctx context.Context, fullImage string) (ImageInfo, error) {
	info := ImageInfo{
		Repository: fullImage,
		Tag:        "latest",
	}
	if info.Repository == "" {
		return info, fmt.Errorf("image repository cannot be empty")
	}
	// ":" ---- nginx:1.21
	if strings.Contains(fullImage, ":") {
		parts := strings.Split(fullImage, ":")
		if len(parts) != 2 {
			return info, fmt.Errorf("invalid image format: %s", fullImage)
		}

		info.Repository = parts[0]
		info.Tag = parts[1]
	}

	return info, nil
}
