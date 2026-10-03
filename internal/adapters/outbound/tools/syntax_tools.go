package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"dezuxk-gateway/internal/core/domain"
)

// ReadSymbolDefinitionTool bóc tách cây cú pháp AST / Tree-sitter để đọc định nghĩa chi tiết của hàm / struct / interface
type ReadSymbolDefinitionTool struct {
	workspace string
}

func NewReadSymbolDefinitionTool(workspace string) *ReadSymbolDefinitionTool {
	return &ReadSymbolDefinitionTool{workspace: workspace}
}

func (t *ReadSymbolDefinitionTool) Name() string { return "read_symbol_definition" }
func (t *ReadSymbolDefinitionTool) Description() string {
	return "Bóc tách cây cú pháp (AST / Tree-sitter) để tìm và đọc chính xác toàn bộ định nghĩa của một hàm (function/method), struct, interface hoặc class theo tên và package mà không cần biết trước số dòng."
}
func (t *ReadSymbolDefinitionTool) Permission() domain.PermissionLevel { return domain.PermissionSafe }

func (t *ReadSymbolDefinitionTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"symbol": {
				"type": "string",
				"description": "Tên hàm, method, struct, interface hoặc class cần tìm (ví dụ: \"ExecuteChatSync\", \"NewRunner\", \"ChatService\", \"RunGraph\")"
			},
			"package_or_dir": {
				"type": "string",
				"description": "Tên package (ví dụ: \"services\", \"agent\", \"domain\") hoặc đường dẫn thư mục để thu hẹp phạm vi tìm kiếm (tùy chọn)"
			},
			"file_path": {
				"type": "string",
				"description": "Đường dẫn tệp tin cụ thể nếu đã biết (tùy chọn)"
			}
		},
		"required": ["symbol"]
	}`)
}

type ReadSymbolArgs struct {
	Symbol       string `json:"symbol"`
	PackageOrDir string `json:"package_or_dir,omitempty"`
	FilePath     string `json:"file_path,omitempty"`
}

type SymbolMatch struct {
	SymbolName string
	Kind       string // "Function", "Method", "Struct", "Interface", "Type"
	Package    string
	FilePath   string
	StartLine  int
	EndLine    int
	DocComment string
	Definition string
}

func (t *ReadSymbolDefinitionTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args ReadSymbolArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("tham số JSON không hợp lệ cho read_symbol_definition: %w", err)
	}
	if strings.TrimSpace(args.Symbol) == "" {
		return "", fmt.Errorf("tham số symbol không được để trống")
	}

	searchRoot, err := resolvePath(ctx, t.workspace, "")
	if err != nil {
		return "", err
	}

	var candidateFiles []string

	if strings.TrimSpace(args.FilePath) != "" {
		targetFile, err := resolvePath(ctx, t.workspace, args.FilePath)
		if err != nil {
			return "", err
		}
		candidateFiles = append(candidateFiles, targetFile)
	} else {
		// Quét toàn bộ file trong workspace
		_ = filepath.Walk(searchRoot, func(p string, info os.FileInfo, err error) error {
			if err != nil || info == nil {
				return nil
			}
			name := info.Name()
			if info.IsDir() {
				if name == ".git" || name == "node_modules" || name == "vendor" || name == ".dezuxk" {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
				candidateFiles = append(candidateFiles, p)
			}
			return nil
		})
	}

	if len(candidateFiles) == 0 {
		return fmt.Sprintf("[Không tìm thấy tệp mã nguồn nào phù hợp trong workspace %s]", searchRoot), nil
	}

	var matches []SymbolMatch

	for _, fpath := range candidateFiles {
		content, err := os.ReadFile(fpath)
		if err != nil {
			continue
		}

		fset := token.NewFileSet()
		node, err := parser.ParseFile(fset, fpath, content, parser.ParseComments)
		if err != nil || node == nil {
			continue
		}

		pkgName := node.Name.Name
		if args.PackageOrDir != "" {
			dir := filepath.Dir(fpath)
			relDir, _ := filepath.Rel(searchRoot, dir)
			matchesDir := strings.Contains(filepath.ToSlash(relDir), args.PackageOrDir) ||
				strings.EqualFold(filepath.Base(dir), args.PackageOrDir)
			matchesPkg := strings.EqualFold(pkgName, args.PackageOrDir)
			if !matchesDir && !matchesPkg {
				continue
			}
		}

		lines := strings.Split(string(content), "\n")

		ast.Inspect(node, func(n ast.Node) bool {
			if n == nil {
				return true
			}

			switch decl := n.(type) {
			case *ast.FuncDecl:
				funcName := decl.Name.Name
				if funcName == args.Symbol {
					startLine := fset.Position(decl.Pos()).Line
					endLine := fset.Position(decl.End()).Line

					kind := "Function"
					recvStr := ""
					if decl.Recv != nil && len(decl.Recv.List) > 0 {
						kind = "Method"
						field := decl.Recv.List[0]
						startRecv := fset.Position(field.Pos()).Offset
						endRecv := fset.Position(field.End()).Offset
						if startRecv >= 0 && endRecv <= len(content) && startRecv < endRecv {
							recvStr = string(content[startRecv:endRecv])
							kind = fmt.Sprintf("Method (Receiver: %s)", recvStr)
						}
					}

					doc := ""
					if decl.Doc != nil {
						doc = decl.Doc.Text()
					}

					codeDef := extractLines(lines, startLine, endLine)
					matches = append(matches, SymbolMatch{
						SymbolName: funcName,
						Kind:       kind,
						Package:    pkgName,
						FilePath:   fpath,
						StartLine:  startLine,
						EndLine:    endLine,
						DocComment: strings.TrimSpace(doc),
						Definition: codeDef,
					})
				}

			case *ast.GenDecl:
				for _, spec := range decl.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok {
						if ts.Name.Name == args.Symbol {
							startLine := fset.Position(decl.Pos()).Line
							endLine := fset.Position(decl.End()).Line

							kind := "Type"
							switch ts.Type.(type) {
							case *ast.StructType:
								kind = "Struct"
							case *ast.InterfaceType:
								kind = "Interface"
							}

							doc := ""
							if decl.Doc != nil {
								doc = decl.Doc.Text()
							} else if ts.Doc != nil {
								doc = ts.Doc.Text()
							}

							codeDef := extractLines(lines, startLine, endLine)
							matches = append(matches, SymbolMatch{
								SymbolName: ts.Name.Name,
								Kind:       kind,
								Package:    pkgName,
								FilePath:   fpath,
								StartLine:  startLine,
								EndLine:    endLine,
								DocComment: strings.TrimSpace(doc),
								Definition: codeDef,
							})
						}
					}
				}
			}
			return true
		})
	}

	if len(matches) == 0 {
		return fmt.Sprintf("[Không tìm thấy định nghĩa cho symbol %q trong package/đường dẫn %q. Hãy kiểm tra lại tên hàm/struct chính xác]", args.Symbol, args.PackageOrDir), nil
	}

	var sb strings.Builder
	for i, m := range matches {
		relPath, _ := filepath.Rel(searchRoot, m.FilePath)
		if relPath == "" {
			relPath = m.FilePath
		}
		sb.WriteString(fmt.Sprintf("=== TÌM THẤY ĐỊNH NGHĨA SYMBOL: %s [%d/%d] ===\n", m.SymbolName, i+1, len(matches)))
		sb.WriteString(fmt.Sprintf("Tệp tin: %s\n", filepath.ToSlash(relPath)))
		sb.WriteString(fmt.Sprintf("Package: %s\n", m.Package))
		sb.WriteString(fmt.Sprintf("Loại: %s\n", m.Kind))
		sb.WriteString(fmt.Sprintf("Vị trí: Dòng %d đến %d (%d dòng)\n", m.StartLine, m.EndLine, m.EndLine-m.StartLine+1))
		if m.DocComment != "" {
			sb.WriteString(fmt.Sprintf("Mô tả: %s\n", m.DocComment))
		}
		sb.WriteString("--------------------------------------------------\n")
		sb.WriteString(m.Definition)
		sb.WriteString("\n\n")
	}

	return strings.TrimSpace(sb.String()), nil
}

func extractLines(allLines []string, start, end int) string {
	if start < 1 {
		start = 1
	}
	if end > len(allLines) {
		end = len(allLines)
	}
	if start > end {
		return ""
	}
	var sb strings.Builder
	for i := start; i <= end; i++ {
		sb.WriteString(fmt.Sprintf("%4d: %s\n", i, strings.TrimRight(allLines[i-1], "\r\n")))
	}
	return sb.String()
}
