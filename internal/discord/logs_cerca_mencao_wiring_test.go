package discord

import (
	"encoding/json"
	"strings"
	"testing"
)

// Este arquivo testa a FIAÇÃO do escape de cerca + AllowedMentions em
// codeBlock/editResponse, pelo caminho PÚBLICO e mais grave: /logs responde
// SEM flag efêmera (o anexo de arquivo grande depende disso), então um
// container cujo stdout contenha ``` e uma menção publica ambos no canal
// onde o comando foi usado.
func TestCmdLogsNaoFechaCercaNemPermiteMencao(t *testing.T) {
	payload := "linha normal\n```\n@everyone [clique aqui](http://exemplo-phishing.invalid)"
	b, rt := newWiringBot(t, payload)

	b.cmdLogs(logsInteraction())

	var achouPatch bool
	for _, body := range rt.bodiesCopy() {
		var payload struct {
			Content         *string `json:"content"`
			AllowedMentions *struct {
				Parse []string `json:"parse"`
			} `json:"allowed_mentions"`
		}
		if json.Unmarshal(body, &payload) != nil || payload.Content == nil {
			continue
		}
		if !strings.Contains(*payload.Content, "@everyone") {
			continue // não é a edição com o log (pode ser o defer inicial)
		}
		achouPatch = true

		if strings.Count(*payload.Content, "```") != 2 {
			t.Fatalf("a cerca de código não ficou fechada nas duas pontas: %q", *payload.Content)
		}
		if payload.AllowedMentions == nil || payload.AllowedMentions.Parse == nil || len(payload.AllowedMentions.Parse) != 0 {
			t.Fatalf("AllowedMentions.Parse não veio como lista vazia: %+v", payload.AllowedMentions)
		}
	}
	if !achouPatch {
		t.Fatal("nenhum corpo com o conteúdo do log foi enviado ao Discord")
	}
}
