package discord

import (
	"fmt"
	"strings"
	"testing"
)

// A regra de cor do audit() era "verde por padrao, colorBusy so para ❌/⚠️":
// todo prefixo novo nascia verde por omissao. Ja custou o ⏳ da recusa
// agregada (29/08) e custava o ⛔ do exec barrado pela allow-list
// (ops.go:182), que saia com a MESMA cor de uma acao bem-sucedida no canal de
// auditoria. Estes dois testes reusam os helpers de exec_audit_wiring_test.go
// (newExecWiringBot/execModalInteraction) — nao redefinem nenhum.

// TestExecBloqueadoAuditaComCorDeAlerta prova que a recusa da allow-list
// audita com colorBusy, nao com a cor de sucesso.
func TestExecBloqueadoAuditaComCorDeAlerta(t *testing.T) {
	b, rt := newExecWiringBot(t, 0)
	b.cfg.AuditChannelID = "canal-auditoria"
	b.cfg.ExecAllowlist = []string{"ls"} // liga a trava; "rm" nao esta na lista

	b.handleModal(execModalInteraction("rm -rf /"))
	b.auditWG.Wait()

	sent := string(rt.all())
	if !strings.Contains(sent, "bloqueado") {
		t.Fatalf("recusa da allow-list nao foi auditada: %q", sent)
	}
	// Comparar pelo campo "color":<n> inteiro, nao por Contains cru do numero
	// (o "30" de colorOK=3066993 casa com qualquer coisa) — mesmo cuidado de
	// ratelimit_audit_wiring_test.go:53-61.
	if !strings.Contains(sent, fmt.Sprintf("\"color\":%d", colorBusy)) {
		t.Fatalf("exec bloqueado auditado com cor de sucesso: %q", sent)
	}
}

// TestExecComSucessoSegueVerde e' a contraprova obrigatoria: o risco da
// inversao e' "tudo virou vermelho". Sem allow-list, o comando roda e tem que
// seguir auditando verde.
func TestExecComSucessoSegueVerde(t *testing.T) {
	b, rt := newExecWiringBot(t, 0)
	b.cfg.AuditChannelID = "canal-auditoria"

	b.handleModal(execModalInteraction("echo oi"))
	b.auditWG.Wait()

	sent := string(rt.all())
	if !strings.Contains(sent, fmt.Sprintf("\"color\":%d", colorOK)) {
		t.Fatalf("exec bem-sucedido devia seguir verde: %q", sent)
	}
}
