package discord

import (
	"strings"
	"testing"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/config"
)

// execAllowed (ops.go) é o único freio do /exec, e até agora não tinha um
// teste que exercitasse suas regras diretamente — os dois testes que o
// tocam (exec_block_color_test.go) verificam a COR do embed de auditoria,
// não o portão. Isso importa porque EXEC_ALLOWLIST está vazia em produção
// (aviso no boot) e passa a ser a única trava assim que for populada.
//
// execAllowed só lê b.cfg: não precisa de sessão, transporte nem dashboard.

func TestExecAllowedExigeTokenExatoNaoPrefixo(t *testing.T) {
	b := &Bot{cfg: &config.Config{ExecAllowlist: []string{"ls"}}}

	if _, ok := b.execAllowed("lsof -i"); ok {
		t.Fatalf("lsof nao deveria passar por prefixo de ls")
	}
	if reason, _ := b.execAllowed("lsof -i"); !strings.Contains(reason, "lsof") {
		t.Fatalf("razao devia citar o token recusado (lsof), veio: %q", reason)
	}

	// Contraprova: sem ela, um execAllowed que bloqueia tudo passaria no
	// teste acima sem provar nada sobre prefixo especificamente.
	if _, ok := b.execAllowed("ls -la"); !ok {
		t.Fatalf("ls -la deveria ser permitido (token exato na lista)")
	}
}

func TestExecAllowedVetaMetacaractereComPrimeiroTokenPermitido(t *testing.T) {
	b := &Bot{cfg: &config.Config{ExecAllowlist: []string{"ls"}}}

	metas := []string{";", "&", "|", "`", "$", ">", "<", "\n"}
	for _, m := range metas {
		cmd := "ls" + m + " rm -rf /"
		reason, ok := b.execAllowed(cmd)
		if ok {
			t.Fatalf("metacaractere %q deveria ser vetado mesmo com token permitido: %q", m, cmd)
		}
		if !strings.Contains(reason, "metacaracteres") {
			t.Fatalf("recusa de %q devia citar metacaracteres (nao token), veio: %q", cmd, reason)
		}
	}
}

func TestExecAllowedListaVaziaLiberaTudo(t *testing.T) {
	b := &Bot{cfg: &config.Config{ExecAllowlist: nil}}

	if _, ok := b.execAllowed("rm -rf /"); !ok {
		t.Fatalf("allowlist vazia deveria liberar qualquer comando")
	}
}

func TestExecAllowedComandoSoDeEspacos(t *testing.T) {
	b := &Bot{cfg: &config.Config{ExecAllowlist: []string{"ls"}}}

	reason, ok := b.execAllowed("   ")
	if ok {
		t.Fatalf("comando so de espacos nao deveria ser permitido")
	}
	if !strings.Contains(reason, "vazio") {
		t.Fatalf("razao devia citar comando vazio, veio: %q", reason)
	}
}
