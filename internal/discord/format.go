package discord

import (
	"fmt"
	"time"
)

// humanBytes formata bytes em unidades legíveis (KiB, MiB, GiB...).
func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// humanDuration formata uma duração como "3d 4h 12m".
func humanDuration(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	d = d.Round(time.Minute)
	days := d / (24 * time.Hour)
	d -= days * 24 * time.Hour
	hours := d / time.Hour
	d -= hours * time.Hour
	mins := d / time.Minute

	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}

// stateEmoji devolve um emoji para o estado do container.
func stateEmoji(state string) string {
	switch state {
	case "running":
		return "🟢"
	case "paused":
		return "⏸️"
	case "restarting":
		return "🔄"
	case "created":
		return "🟡"
	default: // exited, dead, removing...
		return "🔴"
	}
}

// pct formata um percentual com uma casa decimal.
func pct(v float64) string {
	return fmt.Sprintf("%.1f%%", v)
}

// selectEmoji devolve um emoji simples (sem seletor de variação) para as opções
// do select menu, onde emojis "compostos" podem dar problema.
func selectEmoji(state string) string {
	switch state {
	case "running":
		return "🟢"
	case "created", "restarting":
		return "🟡"
	default: // paused, exited, dead...
		return "🔴"
	}
}

// truncate corta a string em n runes (seguro para multibyte).
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
