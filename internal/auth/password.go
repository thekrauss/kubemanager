package auth

import "strings"

// Règle des mots de passe, appliquée par betorun avant tout appel à
// l'annuaire (inscription, réinitialisation, changement). L'annuaire a la
// même politique, en défense en profondeur.
const (
	MinPasswordLength = 12
	MaxPasswordLength = 128
)

// commonPasswords : mots de passe parmi les plus utilisés qui passeraient la
// règle de longueur (la longueur élimine déjà l'essentiel des listes connues).
var commonPasswords = map[string]bool{
	"password1234": true, "password12345": true, "password123456": true, "123456789012": true,
	"1234567890123": true, "qwertyuiop12": true, "azertyuiop12": true, "motdepasse123": true,
	"motdepasse1234": true, "iloveyou1234": true, "welcome12345": true, "administrator": true,
	"changeme1234": true, "aaaaaaaaaaaa": true, "000000000000": true, "111111111111": true,
}

// WeakPassword : vrai si le mot de passe ne respecte pas la règle.
func WeakPassword(email, password string) bool {
	n := len([]rune(password))
	if n < MinPasswordLength || n > MaxPasswordLength {
		return true
	}
	lower := strings.ToLower(password)
	if commonPasswords[lower] {
		return true
	}
	// Un seul caractère répété (ex. « aaaaaaaaaaaaaa »).
	if strings.Count(lower, string([]rune(lower)[0])) == n {
		return true
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email != "" {
		if lower == email {
			return true
		}
		if local, _, ok := strings.Cut(email, "@"); ok && len(local) >= 4 && strings.Contains(lower, local) {
			return true
		}
	}
	return false
}
