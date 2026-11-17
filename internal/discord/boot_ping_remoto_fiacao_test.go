package discord

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestStartChamaPingRemotesNaOrdemDoBoot prova a FIAÇÃO que o teste de
// boot_ping_remoto_log_test.go não alcança: Start() não é seam de teste (o
// openWithRetry abre a sessão do Discord de verdade), então apagar a chamada
// b.pingRemotes() de Start() deixaria a suíte verde e o boot mudo sobre os
// hosts remotos. Aqui a checagem é estrutural, pelo AST de bot.go: Start tem
// de chamar b.pingRemotes() exatamente uma vez, DEPOIS de pingLocalWithRetry
// e ANTES de registerCommands (a ordem do boot que o refactor preservou).
// Escrito pela drenagem de 17/11/2026 (prova por mutação própria).
func TestStartChamaPingRemotesNaOrdemDoBoot(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "bot.go", nil, 0)
	if err != nil {
		t.Fatalf("parse de bot.go: %v", err)
	}
	var start *ast.FuncDecl
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if ok && fd.Name.Name == "Start" && fd.Recv != nil {
			start = fd
		}
	}
	if start == nil {
		t.Fatal("método Start não encontrado em bot.go")
	}

	// Ordem das chamadas b.<metodo>() no corpo de Start, em ordem de fonte.
	var ordem []string
	ast.Inspect(start.Body, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := ce.Fun.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "b" {
				ordem = append(ordem, sel.Sel.Name)
			}
		}
		return true
	})

	pos := func(nome string) (int, int) {
		idx, n := -1, 0
		for i, s := range ordem {
			if s == nome {
				if idx < 0 {
					idx = i
				}
				n++
			}
		}
		return idx, n
	}
	iLocal, _ := pos("pingLocalWithRetry")
	iRem, nRem := pos("pingRemotes")
	iReg, _ := pos("registerCommands")

	// Controle positivo: as âncoras de ordem têm de existir, senão o teste
	// passaria/falharia pelo motivo errado.
	if iLocal < 0 || iReg < 0 {
		t.Fatalf("âncoras do boot ausentes em Start (pingLocalWithRetry=%d registerCommands=%d): %v", iLocal, iReg, ordem)
	}
	if nRem != 1 {
		t.Fatalf("Start deve chamar b.pingRemotes() exatamente 1 vez, chamou %d: %v", nRem, ordem)
	}
	if !(iLocal < iRem && iRem < iReg) {
		t.Fatalf("b.pingRemotes() fora de ordem (local=%d remotos=%d register=%d): %v", iLocal, iRem, iReg, ordem)
	}
}
