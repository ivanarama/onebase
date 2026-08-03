package onec_forms

import (
	"errors"
	"fmt"
	oblog "github.com/ivantit66/onebase/internal/logging"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// CopyResources копирует бинарные ресурсы из 1С-папки Items/* в проект OneBase.
//
// itemsDir указывает на Forms/<FormName>/Ext/Form/Items.
// dstResourcesDir — каталог в проекте OneBase, обычно
//
//	<project>/forms/<entity>/<form_name>/_resources.
//
// Для каждой подпапки (имя = ElementName) сканируются все файлы. Узнаваемые
// (.png, .gif, .svg, .jpg) копируются и регистрируются как IRResource.
// Возвращаются также diagnostics — для отсутствующего itemsDir warns пуст.
func CopyResources(itemsDir, dstResourcesDir string) ([]IRResource, []Warning, error) {
	if itemsDir == "" {
		return nil, nil, nil
	}
	st, err := os.Stat(itemsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	if !st.IsDir() {
		return nil, nil, fmt.Errorf("itemsDir %q не директория", itemsDir)
	}

	entries, err := os.ReadDir(itemsDir)
	if err != nil {
		return nil, nil, err
	}

	if err := os.MkdirAll(dstResourcesDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create %s: %w", dstResourcesDir, err)
	}

	var resources []IRResource
	var warns Warnings

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		elementName := e.Name()
		srcDir := filepath.Join(itemsDir, elementName)

		files, err := os.ReadDir(srcDir)
		if err != nil {
			warns.Add(Warning{
				Severity: SeverityWarn,
				Code:     W013_ResourceMissing,
				Element:  elementName,
				Message:  fmt.Sprintf("не удалось прочитать %s: %v", srcDir, err),
			})
			continue
		}

		dstDir := filepath.Join(dstResourcesDir, elementName)
		dirCreated := false

		for _, f := range files {
			if f.IsDir() {
				continue
			}
			name := f.Name()
			if !isBinaryResource(name) {
				warns.Add(Warning{
					Severity: SeverityInfo,
					Code:     W050_NeedsReview,
					Element:  elementName,
					Field:    name,
					Message:  "файл с неизвестным расширением — пропущен",
				})
				continue
			}

			if !dirCreated {
				if err := os.MkdirAll(dstDir, 0o755); err != nil {
					return resources, []Warning(warns), fmt.Errorf("create %s: %w", dstDir, err)
				}
				dirCreated = true
			}

			srcPath := filepath.Join(srcDir, name)
			dstPath := filepath.Join(dstDir, name)
			if err := copyFile(srcPath, dstPath); err != nil {
				warns.Add(Warning{
					Severity: SeverityWarn,
					Code:     W013_ResourceMissing,
					Element:  elementName,
					Field:    name,
					Message:  fmt.Sprintf("копирование не удалось: %v", err),
				})
				continue
			}

			// относительный путь от каталога формы (parent от _resources)
			rel, err := filepath.Rel(filepath.Dir(dstResourcesDir), dstPath)
			if err != nil {
				rel = filepath.Join(filepath.Base(dstResourcesDir), elementName, name)
			}
			// нормализуем разделители на forward slashes для YAML
			rel = filepath.ToSlash(rel)

			resources = append(resources, IRResource{
				ElementName:  elementName,
				Path:         rel,
				OriginalName: name,
			})
		}
	}

	return resources, []Warning(warns), nil
}

// isBinaryResource проверяет, что расширение файла известно как
// картинка/иконка ресурса формы 1С.
func isBinaryResource(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".png", ".gif", ".svg", ".jpg", ".jpeg", ".bmp", ".ico":
		return true
	}
	return false
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer oblog.CloseQuiet("onec_forms", "исходный файл", in)
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	// Close пишущей стороны возвращаем: он сбрасывает буфер, и без проверки
	// усечённый ресурс формы сошёл бы за скопированный.
	if _, err := io.Copy(out, in); err != nil {
		return errors.Join(err, out.Close())
	}
	return out.Close()
}

// AttachResourcesToForm проходит по дереву элементов формы и проставляет
// поля Picture/ValuesPicture у элементов, имена которых совпадают с
// ElementName в ресурсах. Если у элемента уже задано picture: stdpic:X —
// не трогаем (стандартная иконка).
func AttachResourcesToForm(form *IRForm, resources []IRResource) {
	if form == nil || len(resources) == 0 {
		return
	}
	byElement := make(map[string][]IRResource, len(resources))
	for _, r := range resources {
		byElement[r.ElementName] = append(byElement[r.ElementName], r)
	}
	var attach func(*IRElement)
	attach = func(el *IRElement) {
		if el == nil {
			return
		}
		if list, ok := byElement[el.Name]; ok {
			for _, r := range list {
				switch r.OriginalName {
				case "Picture.png", "Picture.gif", "Picture.svg":
					if el.Picture == "" {
						el.Picture = r.Path
					}
				case "ValuesPicture.png", "ValuesPicture.gif":
					if el.Values == "" {
						el.Values = r.Path
					}
				}
			}
		}
		for _, c := range el.Children {
			attach(c)
		}
	}
	for _, el := range form.Elements {
		attach(el)
	}
	form.Resources = append(form.Resources, resources...)
}
