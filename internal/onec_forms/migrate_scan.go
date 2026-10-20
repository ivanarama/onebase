package onec_forms

// This scanner measures source usage, not runtime compatibility. In particular,
// the historical W040 pattern descriptions are not compatibility verdicts.
import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

type ScanVolume struct {
	Modules int `json:"modules"`
	Lines   int `json:"lines"`
}

type ScanFrequency struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
	Files int    `json:"files"`
}

type ScanConcentration struct {
	Top     int     `json:"top"`
	Calls   int     `json:"calls"`
	Percent float64 `json:"percent"`
}

type ScanSubsystem struct {
	Name    string          `json:"name"`
	Modules int             `json:"modules"`
	Calls   int             `json:"calls"`
	Methods []ScanFrequency `json:"methods"`
}

type ScanModule struct {
	Path     string `json:"path"`
	Encoding string `json:"encoding"`
	Lines    int    `json:"lines"`
	BSP      bool   `json:"bsp"`
}

// ExportScan contains aggregates and relative paths, never client source text.
// Slice order is deterministic; there are no timestamps or absolute paths.
type ExportScan struct {
	SchemaVersion              int                 `json:"schema_version"`
	Total                      ScanVolume          `json:"total"`
	BSP                        ScanVolume          `json:"bsp"`
	Application                ScanVolume          `json:"application"`
	BSPLinePercent             float64             `json:"bsp_line_percent"`
	BSPSubsystems              int                 `json:"bsp_subsystems"`
	BSPCommonModules           int                 `json:"bsp_common_modules"`
	UsedBSPCommonModules       int                 `json:"used_bsp_common_modules"`
	UnusedBSPCommonModules     int                 `json:"unused_bsp_common_modules"`
	BSPCalls                   int                 `json:"bsp_calls"`
	RareMethods                int                 `json:"methods_called_once_or_twice"`
	Modules                    []ScanFrequency     `json:"calls_by_module"`
	Methods                    []ScanFrequency     `json:"calls_by_method"`
	MethodNames                []ScanFrequency     `json:"calls_by_method_name"`
	Concentration              []ScanConcentration `json:"concentration"`
	Subsystems                 []ScanSubsystem     `json:"subsystem_entry_points"`
	RawW040                    []ScanFrequency     `json:"raw_w040_application"`
	RawW040Profile             string              `json:"raw_w040_profile"`
	RawW040Occurrences         int                 `json:"raw_w040_occurrences"`
	CompatibilityStatus        string              `json:"compatibility_status"`
	ConfirmedIncompatibilities []ScanFrequency     `json:"confirmed_incompatibilities"`
	Sources                    []ScanModule        `json:"sources"`
}

type scanSubsystem struct {
	key      string
	objects  []string
	children []string
}

var scanCallRE = regexp.MustCompile(`([\p{L}_][\p{L}\p{N}_]*)\s*\.\s*([\p{L}_][\p{L}\p{N}_]*)\s*\(`)

// Freeze the measurement vocabulary used in plan 179. Import diagnostics have
// since become signature-aware; using their current warning list would silently
// change the historical denominator whenever a supported signature is added.
var scanW040Fragments = []string{
	"Новый Запрос", "Новый ТаблицаЗначений", "Новый ХранилищеЗначения",
	"Новый ОписаниеОповещения", "Новый Структура", "Новый Соответствие", "Новый Массив",
	"ПоказатьВопрос(", "ПоказатьПредупреждение(", "ПоказатьВводЗначения(", "Состояние(",
	"Сообщить(", "ВызватьИсключение", "СтатусСообщения", "ОбщегоНазначения.",
	"ОбщегоНазначенияКлиент.", "Метаданные.", "Документы.", "Справочники.",
	"РегистрыСведений.", "РегистрыНакопления.", "НСтр(", "СтрШаблон(",
	"ЭтоАдресВременногоХранилища(", "ПолучитьИзВременногоХранилища(",
	"НачатьТранзакцию(", "ЗафиксироватьТранзакцию(", "ОтменитьТранзакцию(",
	"ХранилищеОбщихНастроекСохранить", "ХранилищеОбщихНастроекЗагрузить",
}

// ScanExport reads a Designer XML directory export. BSP membership is the
// transitive Content of Subsystem.СтандартныеПодсистемы, never a module-name
// heuristic. Calls are lexical qualified-call candidates (no name resolution).
// Raw W040 counts preserve the legacy case-sensitive fragments, counting every
// occurrence on non-comment lines, including strings and trailing comments.
func ScanExport(root, encoding string) (*ExportScan, error) {
	if !validScanEncoding(encoding) {
		return nil, fmt.Errorf("unknown encoding %q", encoding)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("expected a Designer XML export directory: %s", root)
	}
	var bslPaths, subsystemPaths []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in export: %s", path)
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.EqualFold(filepath.Ext(path), ".bsl") {
			bslPaths = append(bslPaths, rel)
		}
		if strings.HasPrefix(rel, "Subsystems/") && strings.EqualFold(filepath.Ext(path), ".xml") && filepath.Base(filepath.Dir(path)) == "Subsystems" {
			subsystemPaths = append(subsystemPaths, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(bslPaths) == 0 {
		return nil, fmt.Errorf("no BSL modules in export")
	}
	sort.Strings(bslPaths)
	sort.Strings(subsystemPaths)
	subsystems := map[string]scanSubsystem{}
	for _, path := range subsystemPaths {
		s, err := readScanSubsystem(filepath.Join(root, filepath.FromSlash(path)), path, encoding)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		key := strings.ToLower(s.key)
		if _, exists := subsystems[key]; exists {
			return nil, fmt.Errorf("duplicate subsystem %s", s.key)
		}
		subsystems[key] = s
	}
	// Each subsystem's set includes its descendants, for entry-point summaries.
	sets := map[string]map[string]bool{}
	active := map[string]bool{}
	var collect func(string) (map[string]bool, error)
	collect = func(key string) (map[string]bool, error) {
		key = strings.ToLower(key)
		if active[key] {
			return nil, fmt.Errorf("cyclic subsystem reference: %s", key)
		}
		if set, ok := sets[key]; ok {
			return set, nil
		}
		s, ok := subsystems[key]
		if !ok {
			return nil, fmt.Errorf("missing subsystem metadata: %s", key)
		}
		active[key] = true
		set := map[string]bool{}
		for _, object := range s.objects {
			if strings.HasPrefix(strings.ToLower(object), "subsystem.") {
				child, err := collect(object)
				if err != nil {
					return nil, err
				}
				for o := range child {
					set[o] = true
				}
			} else {
				set[strings.ToLower(object)] = true
			}
		}
		for _, childKey := range s.children {
			child, err := collect(childKey)
			if err != nil {
				return nil, err
			}
			for o := range child {
				set[o] = true
			}
		}
		active[key] = false
		sets[key] = set
		return set, nil
	}
	bspKey := strings.ToLower("Subsystem.СтандартныеПодсистемы")
	bspObjects := map[string]bool{}
	if _, ok := subsystems[bspKey]; ok {
		bspObjects, err = collect(bspKey)
		if err != nil {
			return nil, err
		}
	}
	bspCommon := map[string]string{}
	// Metadata references supply membership, files supply canonical display names.
	for _, path := range bslPaths {
		object := scanObject(path)
		if strings.HasPrefix(object, "CommonModule.") && bspObjects[strings.ToLower(object)] {
			bspCommon[strings.ToLower(strings.TrimPrefix(object, "CommonModule."))] = strings.TrimPrefix(object, "CommonModule.")
		}
	}
	// Keep modules with metadata but no source in the unused-module denominator.
	for object := range bspObjects {
		if strings.HasPrefix(object, "commonmodule.") {
			name := strings.TrimPrefix(object, "commonmodule.")
			if _, ok := bspCommon[name]; !ok {
				bspCommon[name] = name
			}
		}
	}
	r := &ExportScan{SchemaVersion: 1, Modules: []ScanFrequency{}, Methods: []ScanFrequency{}, MethodNames: []ScanFrequency{}, Subsystems: []ScanSubsystem{}, Sources: []ScanModule{}, RawW040: []ScanFrequency{}, RawW040Profile: "plan179-w040-v1", CompatibilityStatus: "not_evaluated", ConfirmedIncompatibilities: []ScanFrequency{}}
	moduleCalls, methodCalls, rawCounts := map[string]int{}, map[string]int{}, map[string]int{}
	moduleFiles, methodFiles, rawFiles := map[string]map[string]bool{}, map[string]map[string]bool{}, map[string]map[string]bool{}
	methodNames := map[string]string{}
	shortCounts, shortNames := map[string]int{}, map[string]string{}
	shortFiles := map[string]map[string]bool{}
	for _, path := range bslPaths {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return nil, err
		}
		source, enc, err := decodeScanText(data, encoding)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		source = strings.ReplaceAll(strings.ReplaceAll(source, "\r\n", "\n"), "\r", "\n")
		lines := strings.Count(source, "\n")
		if source != "" && !strings.HasSuffix(source, "\n") {
			lines++
		}
		bsp := bspObjects[strings.ToLower(scanObject(path))]
		r.Sources = append(r.Sources, ScanModule{path, enc, lines, bsp})
		r.Total.Modules++
		r.Total.Lines += lines
		if bsp {
			r.BSP.Modules++
			r.BSP.Lines += lines
			continue
		}
		r.Application.Modules++
		r.Application.Lines += lines
		for _, line := range strings.Split(source, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, fragment := range scanW040Fragments {
				if n := strings.Count(line, fragment); n > 0 {
					rawCounts[fragment] += n
					r.RawW040Occurrences += n
					scanAddFile(rawFiles, fragment, path)
				}
			}
		}
		code := scanCodeOnly(source)
		for _, match := range scanCallRE.FindAllStringSubmatchIndex(code, -1) {
			// obj.Module.Method() is not a direct Module.Method() invocation.
			prefix := strings.TrimSpace(code[:match[0]])
			if strings.HasSuffix(prefix, ".") {
				continue
			}
			moduleKey := strings.ToLower(code[match[2]:match[3]])
			moduleName, ok := bspCommon[moduleKey]
			if !ok {
				continue
			}
			method := code[match[4]:match[5]]
			methodKey := moduleKey + "." + strings.ToLower(method)
			shortKey := strings.ToLower(method)
			shortCounts[shortKey]++
			if old, ok := shortNames[shortKey]; !ok || method < old {
				shortNames[shortKey] = method
			}
			scanAddFile(shortFiles, shortKey, path)
			name := moduleName + "." + method
			if old, ok := methodNames[methodKey]; !ok || name < old {
				methodNames[methodKey] = name
			}
			moduleCalls[moduleKey]++
			methodCalls[methodKey]++
			r.BSPCalls++
			scanAddFile(moduleFiles, moduleKey, path)
			scanAddFile(methodFiles, methodKey, path)
		}
	}
	r.BSPLinePercent = scanPercent(r.BSP.Lines, r.Total.Lines)
	r.BSPCommonModules = len(bspCommon)
	r.UsedBSPCommonModules = len(moduleCalls)
	r.UnusedBSPCommonModules = len(bspCommon) - len(moduleCalls)
	for key, count := range moduleCalls {
		r.Modules = append(r.Modules, ScanFrequency{bspCommon[key], count, len(moduleFiles[key])})
	}
	for key, count := range methodCalls {
		r.Methods = append(r.Methods, ScanFrequency{methodNames[key], count, len(methodFiles[key])})
		if count <= 2 {
			r.RareMethods++
		}
	}
	for key, count := range shortCounts {
		r.MethodNames = append(r.MethodNames, ScanFrequency{shortNames[key], count, len(shortFiles[key])})
	}
	for _, fragment := range scanW040Fragments {
		r.RawW040 = append(r.RawW040, ScanFrequency{fragment, rawCounts[fragment], len(rawFiles[fragment])})
	}
	scanSortFrequencies(r.Modules)
	scanSortFrequencies(r.Methods)
	scanSortFrequencies(r.MethodNames)
	scanSortFrequencies(r.RawW040)
	for _, top := range []int{10, 25, 50, 100} {
		calls := 0
		for i, method := range r.Methods {
			if i >= top {
				break
			}
			calls += method.Count
		}
		r.Concentration = append(r.Concentration, ScanConcentration{top, calls, scanPercent(calls, r.BSPCalls)})
	}
	for key, objects := range sets {
		// Only descendants actually traversed from the BSP root are in sets.
		s := ScanSubsystem{Name: subsystems[key].key, Methods: []ScanFrequency{}}
		for object := range objects {
			if strings.HasPrefix(object, "commonmodule.") {
				s.Modules++
			}
		}
		for method, count := range methodCalls {
			module := strings.SplitN(method, ".", 2)[0]
			if objects["commonmodule."+module] {
				s.Calls += count
				s.Methods = append(s.Methods, ScanFrequency{methodNames[method], count, len(methodFiles[method])})
			}
		}
		scanSortFrequencies(s.Methods)
		r.Subsystems = append(r.Subsystems, s)
	}
	sort.Slice(r.Subsystems, func(i, j int) bool { return r.Subsystems[i].Name < r.Subsystems[j].Name })
	r.BSPSubsystems = len(r.Subsystems)
	return r, nil
}

func scanAddFile(files map[string]map[string]bool, key, path string) {
	if files[key] == nil {
		files[key] = map[string]bool{}
	}
	files[key][path] = true
}

func scanPercent(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return 100 * float64(n) / float64(total)
}

func scanSortFrequencies(rows []ScanFrequency) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		return rows[i].Name < rows[j].Name
	})
}

// The first metadata object owns all modules below it (forms included).
var scanObjectKinds = map[string]string{
	"CommonModules": "CommonModule", "Catalogs": "Catalog", "Documents": "Document",
	"Reports": "Report", "DataProcessors": "DataProcessor", "CommonForms": "CommonForm",
	"CommonCommands": "CommonCommand", "HTTPServices": "HTTPService", "WebServices": "WebService",
	"InformationRegisters": "InformationRegister", "AccumulationRegisters": "AccumulationRegister",
	"AccountingRegisters": "AccountingRegister", "CalculationRegisters": "CalculationRegister",
	"ChartsOfAccounts": "ChartOfAccounts", "ChartsOfCharacteristicTypes": "ChartOfCharacteristicTypes",
	"ChartsOfCalculationTypes": "ChartOfCalculationTypes", "BusinessProcesses": "BusinessProcess",
	"Tasks": "Task", "SessionParameters": "SessionParameter", "Constants": "Constant",
	"ExchangePlans": "ExchangePlan", "DocumentJournals": "DocumentJournal", "Enums": "Enum",
	"SettingsStorages": "SettingsStorage", "ExternalDataSources": "ExternalDataSource",
	"IntegrationServices": "IntegrationService", "Sequences": "Sequence",
}

func scanObject(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		return ""
	}
	if kind, ok := scanObjectKinds[parts[0]]; ok {
		return kind + "." + parts[1]
	}
	return ""
}

func readScanSubsystem(path, rel, encoding string) (scanSubsystem, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return scanSubsystem{}, err
	}
	text, _, err := decodeScanText(data, encoding)
	if err != nil {
		return scanSubsystem{}, err
	}
	parts := strings.Split(strings.TrimSuffix(rel, filepath.Ext(rel)), "/")
	var names []string
	// Designer paths alternate service directories and object names. A name
	// may itself be Subsystems, so skip directory positions, not that text.
	for i := 1; i < len(parts); i += 2 {
		names = append(names, "Subsystem", parts[i])
	}
	// Designer references repeat Subsystem at every level: the path
	// Subsystems/A/Subsystems/B.xml denotes Subsystem.A.Subsystem.B.
	s := scanSubsystem{key: strings.Join(names, ".")}
	d := xml.NewDecoder(strings.NewReader(text))
	// Input was decoded strictly before XML parsing, including its declaration.
	d.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }
	var stack []string
	foundSubsystem := false
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return s, err
		}
		switch t := token.(type) {
		case xml.StartElement:
			parent := ""
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			if parent == "MetaDataObject" && t.Name.Local == "Subsystem" {
				foundSubsystem = true
			}
			if (parent == "Content" || parent == "Contents") && t.Name.Local == "Item" {
				var value string
				if err := d.DecodeElement(&value, &t); err != nil {
					return s, err
				}
				value = strings.TrimSpace(value)
				if value != "" {
					s.objects = append(s.objects, value)
				}
				continue
			}
			if parent == "ChildObjects" && t.Name.Local == "Subsystem" {
				var value string
				if err := d.DecodeElement(&value, &t); err != nil {
					return s, err
				}
				value = strings.TrimSpace(value)
				if value != "" {
					s.children = append(s.children, s.key+".Subsystem."+value)
				}
				continue
			}
			stack = append(stack, t.Name.Local)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if !foundSubsystem {
		return s, fmt.Errorf("expected Subsystem metadata object")
	}
	return s, nil
}

func validScanEncoding(enc string) bool {
	switch enc {
	case "auto", "utf-8", "windows-1251", "utf-16le", "utf-16be":
		return true
	}
	return false
}

func decodeScanText(data []byte, enc string) (string, string, error) {
	detected := ""
	switch {
	case bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}):
		detected = "utf-8"
		data = data[3:]
	case bytes.HasPrefix(data, []byte{0xff, 0xfe}):
		detected = "utf-16le"
		data = data[2:]
	case bytes.HasPrefix(data, []byte{0xfe, 0xff}):
		detected = "utf-16be"
		data = data[2:]
	}
	if detected != "" {
		if enc != "auto" && enc != detected {
			return "", "", fmt.Errorf("BOM conflicts with encoding %s", enc)
		}
		enc = detected
	}
	if enc == "auto" {
		if utf8.Valid(data) {
			enc = "utf-8"
		} else {
			enc = "windows-1251"
		}
	}
	switch enc {
	case "utf-8":
		if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
			return "", "", fmt.Errorf("invalid UTF-8")
		}
		return string(data), enc, nil
	case "windows-1251":
		if bytes.Contains(data, []byte{0x98}) {
			return "", "", fmt.Errorf("undefined Windows-1251 byte 0x98")
		}
		decoded, err := charmap.Windows1251.NewDecoder().Bytes(data)
		return string(decoded), enc, err
	case "utf-16le", "utf-16be":
		if len(data)%2 != 0 {
			return "", "", fmt.Errorf("odd UTF-16 byte count")
		}
		var order binary.ByteOrder = binary.LittleEndian
		if enc == "utf-16be" {
			order = binary.BigEndian
		}
		units := make([]uint16, len(data)/2)
		for i := range units {
			units[i] = order.Uint16(data[i*2:])
		}
		for i := 0; i < len(units); i++ {
			v := units[i]
			if v >= 0xd800 && v <= 0xdbff {
				if i+1 >= len(units) || units[i+1] < 0xdc00 || units[i+1] > 0xdfff {
					return "", "", fmt.Errorf("invalid UTF-16 surrogate")
				}
				i++
			} else if v >= 0xdc00 && v <= 0xdfff {
				return "", "", fmt.Errorf("unpaired UTF-16 surrogate")
			}
		}
		return string(utf16.Decode(units)), enc, nil
	}
	return "", "", fmt.Errorf("unknown encoding %q", enc)
}

// Mask strings (including escaped quotes and multiline BSL strings) and //
// comments with spaces. Byte offsets remain valid for lexical call matching.
func scanCodeOnly(source string) string {
	data := []byte(source)
	inString, inComment := false, false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inComment {
			if c == '\n' {
				inComment = false
			} else {
				data[i] = ' '
			}
			continue
		}
		if inString {
			if c == '"' {
				if i+1 < len(data) && data[i+1] == '"' {
					data[i] = ' '
					i++
					data[i] = ' '
					continue
				}
				inString = false
			}
			if c != '\n' {
				data[i] = ' '
			}
			continue
		}
		if c == '"' {
			inString = true
			data[i] = ' '
			continue
		}
		if c == '/' && i+1 < len(data) && data[i+1] == '/' {
			inComment = true
			data[i] = ' '
			i++
			data[i] = ' '
		}
	}
	return string(data)
}
