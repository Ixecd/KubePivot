package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// CommandInfo 存储解析出的命令信息
type CommandInfo struct {
	Name     string
	SubCmd   string
	Flags    []FlagInfo
	FuncName string
}

type FlagInfo struct {
	Name    string
	Default string
	Usage   string
}

// InterfaceInfo 存储接口信息
type InterfaceInfo struct {
	Package string
	Name    string
	Methods []string
}

// FuncInfo 存储公开函数信息
type FuncInfo struct {
	Package string
	Name    string
	Params  string
	Results string
}

// StructInfo 存储公开结构体信息
type StructInfo struct {
	Package string
	Name    string
	Fields  []string
}

func main() {
	// ============================================================
	// 第一部分：扫描 cmd/kp 下的命令与 flag
	// ============================================================
	fmt.Println("================================================================")
	fmt.Println("  乾枢 (v3.0) 系统审计报告 — 命令全景扫描")
	fmt.Println("================================================================")
	fmt.Println()
	fmt.Println("## 1. CLI 命令入口 (cmd/kp/main.go)")
	fmt.Println()

	// 解析 main.go 中的 switch-case 路由
	mainFile := "cmd/kp/main.go"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, mainFile, nil, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "解析 %s 失败: %v\n", mainFile, err)
		os.Exit(1)
	}

	commands := findMainCommands(f)
	for _, c := range commands {
		fmt.Printf("  kp %-25s → %s()\n", c.Name, c.FuncName)
	}

	fmt.Println()
	fmt.Println("## 2. CLI 子命令与 Flag 定义")
	fmt.Println()

	// 遍历 cmd/kp 下所有 .go 文件，提取 flag 注册
	_ = filepath.Walk("cmd/kp", func(path string, info os.FileInfo, err error) error {
		if err != nil || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil
		}

		base := strings.TrimSuffix(filepath.Base(path), ".go")
		cmds := findFlagDefinitions(f, base)
		for _, c := range cmds {
			fmt.Printf("  [%s] %s", base, c.SubCmd)
			if len(c.Flags) > 0 {
				fmt.Println()
				for _, fl := range c.Flags {
					fmt.Printf("    --%-30s default=%-12s  %s\n", fl.Name, fl.Default, fl.Usage)
				}
			} else {
				fmt.Println("  (无额外 flag)")
			}
			fmt.Println()
		}
		return nil
	})

	// ============================================================
	// 第二部分：扫描 internal/ 下的包结构与核心接口
	// ============================================================
	fmt.Println("================================================================")
	fmt.Println("  乾枢 (v3.0) 系统审计报告 — 内部包结构")
	fmt.Println("================================================================")
	fmt.Println()

	var interfaces []InterfaceInfo
	var functions []FuncInfo
	var structures []StructInfo

	_ = filepath.Walk("internal", func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() {
			return nil
		}
		fset := token.NewFileSet()
		pkgs, err := parser.ParseDir(fset, path, nil, 0)
		if err != nil {
			return nil
		}

		for _, pkg := range pkgs {
			for _, file := range pkg.Files {
				// 只扫描非测试文件
				if strings.HasSuffix(fset.File(file.Pos()).Name(), "_test.go") {
					continue
				}
				pkgName := filepath.Base(path)
				for _, decl := range file.Decls {
					switch d := decl.(type) {
					case *ast.GenDecl:
						for _, spec := range d.Specs {
							if ts, ok := spec.(*ast.TypeSpec); ok {
								switch t := ts.Type.(type) {
								case *ast.InterfaceType:
									iface := InterfaceInfo{
										Package: pkgName,
										Name:    ts.Name.Name,
									}
									if t.Methods != nil {
										for _, m := range t.Methods.List {
											if len(m.Names) > 0 {
												iface.Methods = append(iface.Methods, m.Names[0].Name)
											}
										}
									}
									interfaces = append(interfaces, iface)
								case *ast.StructType:
									st := StructInfo{
										Package: pkgName,
										Name:    ts.Name.Name,
									}
									for _, field := range t.Fields.List {
										if len(field.Names) > 0 {
											st.Fields = append(st.Fields, field.Names[0].Name)
										}
									}
									structures = append(structures, st)
								}
							}
						}
					case *ast.FuncDecl:
						if d.Name.IsExported() {
							fn := FuncInfo{
								Package: pkgName,
								Name:    d.Name.Name,
							}
							if d.Type.Params != nil {
								fn.Params = formatFieldList(d.Type.Params)
							}
							if d.Type.Results != nil {
								fn.Results = formatFieldList(d.Type.Results)
							}
							functions = append(functions, fn)
						}
					}
				}
			}
		}
		return nil
	})

	fmt.Println("## 3. 核心接口定义 (interface)")
	fmt.Println()
	for _, iface := range interfaces {
		fmt.Printf("  [%s] %s\n", iface.Package, iface.Name)
		for _, m := range iface.Methods {
			fmt.Printf("    - %s()\n", m)
		}
		fmt.Println()
	}

	fmt.Println("## 4. 关键公开函数 (func)")
	fmt.Println()
	for _, fn := range functions {
		fmt.Printf("  [%s] %-35s %-10s → %-10s\n", fn.Package, fn.Name+"()", fn.Params, fn.Results)
	}
	fmt.Println()

	fmt.Println("## 5. 关键公开结构体 (struct)")
	fmt.Println()
	for _, st := range structures {
		if len(st.Fields) > 0 {
			fmt.Printf("  [%s] %s\n", st.Package, st.Name)
			for _, f := range st.Fields {
				fmt.Printf("    .%s\n", f)
			}
			fmt.Println()
		}
	}

	fmt.Println("================================================================")
	fmt.Printf("  审计完成。共发现 %d 个接口、%d 个公开函数、%d 个结构体。\n",
		len(interfaces), len(functions), len(structures))
}

// findMainCommands 解析 main.go 中的命令路由
func findMainCommands(f *ast.File) []CommandInfo {
	var commands []CommandInfo
	ast.Inspect(f, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.SwitchStmt:
			// 找到 switch os.Args[1] 或类似表达
			for _, caseClause := range stmt.Body.List {
				if cc, ok := caseClause.(*ast.CaseClause); ok {
					for _, expr := range cc.List {
						if bl, ok := expr.(*ast.BasicLit); ok {
							name := strings.Trim(bl.Value, `"`)
							// 查找对应的处理函数
							for _, st := range cc.Body {
								if es, ok := st.(*ast.ExprStmt); ok {
									if call, ok := es.X.(*ast.CallExpr); ok {
										if ident, ok := call.Fun.(*ast.Ident); ok {
											commands = append(commands, CommandInfo{
												Name:     name,
												FuncName: ident.Name,
											})
										}
									}
								}
							}
						}
					}
				}
			}
		}
		return true
	})
	return commands
}

// findFlagDefinitions 在文件中查找 flag 定义
func findFlagDefinitions(f *ast.File, base string) []CommandInfo {
	var commands []CommandInfo

	ast.Inspect(f, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.ValueSpec:
			for _, val := range stmt.Values {
				if call, ok := val.(*ast.CallExpr); ok {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
						if sel.Sel.Name == "StringVar" || sel.Sel.Name == "BoolVar" ||
							sel.Sel.Name == "IntVar" || sel.Sel.Name == "Float64Var" ||
							sel.Sel.Name == "DurationVar" {
							flag := FlagInfo{}
							if len(stmt.Names) > 0 {
								flag.Name = stmt.Names[0].Name
							}
							for i, arg := range call.Args {
								if bl, ok := arg.(*ast.BasicLit); ok {
									str := strings.Trim(bl.Value, `"`)
									switch i {
									case 0:
										flag.Name = str
									case 1:
										flag.Default = str
									case 2:
										flag.Usage = str
									}
								}
							}
							cmd := CommandInfo{
								SubCmd: base,
								Flags:  []FlagInfo{flag},
							}
							commands = append(commands, cmd)
						}
					}
				}
			}
		}
		return true
	})
	return commands
}

// formatFieldList 格式化参数/返回值列表
func formatFieldList(fl *ast.FieldList) string {
	if fl == nil {
		return ""
	}
	var parts []string
	for _, f := range fl.List {
		if len(f.Names) > 0 {
			parts = append(parts, f.Names[0].Name)
		}
	}
	return strings.Join(parts, ", ")
}
