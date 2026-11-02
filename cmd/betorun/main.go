// betorun : ligne de commande de la plateforme betorun (binaire distribué aux
// utilisateurs). Le code est dans internal/cli ; le binaire du serveur
// l'expose aussi via « kubemanager betocli ».
package main

import (
	"os"

	"github.com/thekrauss/kubemanager/internal/cli"
)

func main() { os.Exit(cli.Execute("betorun", os.Args[1:])) }
