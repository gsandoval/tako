package gcp

import (
	"slices"
	"strconv"
	"strings"
)

type runGCSVolume struct {
	Bucket   string `json:"bucket"`
	ReadOnly bool   `json:"readOnly,omitempty"`
}

type runEmptyDirVolume struct {
	Medium    string `json:"medium,omitempty"`
	SizeLimit string `json:"sizeLimit,omitempty"`
}

type runVolume struct {
	Name     string             `json:"name"`
	GCS      *runGCSVolume      `json:"gcs,omitempty"`
	EmptyDir *runEmptyDirVolume `json:"emptyDir,omitempty"`
}

type runVolumeMount struct {
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
}

// secondsOf parses Cloud Run's duration form ("300s") back to an int,
// the shape the desired spec carries.
func secondsOf(d string) int {
	n, err := strconv.Atoi(strings.TrimSuffix(d, "s"))
	if err != nil {
		return 0
	}
	return n
}

func reconstructMounts(volumes []runVolume, vmounts []runVolumeMount) []any {
	if len(volumes) == 0 {
		return nil
	}
	mountPaths := map[string]string{}
	for _, vm := range vmounts {
		mountPaths[vm.Name] = vm.MountPath
	}
	var out []any
	for _, v := range volumes {
		if v.EmptyDir != nil {
			out = append(out, map[string]any{
				"name":      v.Name,
				"mountPath": mountPaths[v.Name],
				"ephemeral": true,
				"sizeLimit": v.EmptyDir.SizeLimit,
			})
		} else if v.GCS != nil {
			out = append(out, map[string]any{
				"name":      v.Name,
				"mountPath": mountPaths[v.Name],
				"ephemeral": false,
				"bucket":    v.GCS.Bucket,
				"readOnly":  v.GCS.ReadOnly,
			})
		}
	}
	slices.SortFunc(out, func(a, b any) int {
		na, _ := a.(map[string]any)["name"].(string)
		nb, _ := b.(map[string]any)["name"].(string)
		return strings.Compare(na, nb)
	})
	return out
}

func buildVolumesAndMounts(spec map[string]any) ([]map[string]any, []map[string]any) {
	raw, ok := spec["mounts"].([]any)
	if !ok || len(raw) == 0 {
		return nil, nil
	}
	var volumes []map[string]any
	var vmounts []map[string]any
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		mountPath, _ := m["mountPath"].(string)
		ephemeral, _ := m["ephemeral"].(bool)
		if ephemeral {
			sizeLimit, _ := m["sizeLimit"].(string)
			volumes = append(volumes, map[string]any{
				"name": name,
				"emptyDir": map[string]any{
					"medium":    "MEMORY",
					"sizeLimit": sizeLimit,
				},
			})
		} else {
			bucket, _ := m["bucket"].(string)
			readOnly, _ := m["readOnly"].(bool)
			volumes = append(volumes, map[string]any{
				"name": name,
				"gcs": map[string]any{
					"bucket":   bucket,
					"readOnly": readOnly,
				},
			})
		}
		vmounts = append(vmounts, map[string]any{
			"name":      name,
			"mountPath": mountPath,
		})
	}
	return volumes, vmounts
}
