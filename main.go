//go:build windows

// RealPassGAL – 1. lépés: kulcsok (identitás, partnerek, kulcskönyv).
package main

import (
	"os"
	"path/filepath"

	"github.com/lxn/walk"

	"realpassgal/core"
)

const appTitle = "RealPassGAL"

// app – a bejelentkezett felhasználó állapota.
type app struct {
	dir      string
	password string
	me       *core.Identity
	partners []*core.PublicKey
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "-kepek" {
		runPhotoMode()
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "-demo" {
		runDemoMode()
		return
	}
	dir, err := core.DataDir()
	if err != nil {
		walk.MsgBox(nil, appTitle, "Nem érem el az adatmappát:\n"+err.Error(), walk.MsgBoxIconError)
		return
	}
	a := &app{dir: dir}
	idPath := filepath.Join(dir, core.IdentityFile)

	if _, err := os.Stat(idPath); os.IsNotExist(err) {
		if !a.newIdentityWizard(nil) {
			return
		}
	} else if !a.loginDialog(nil) {
		return
	}
	a.mainWindow()
}

func (a *app) idPath() string       { return filepath.Join(a.dir, core.IdentityFile) }
func (a *app) partnersPath() string { return filepath.Join(a.dir, core.PartnersFile) }

func (a *app) loadPartners() error {
	ps, err := core.LoadPartners(a.partnersPath(), a.me.StorageKey)
	a.partners = ps
	return err
}

func (a *app) savePartners() error {
	r, err := core.NewRNG(a.me.Reserve)
	if err != nil {
		return err
	}
	return core.SavePartners(a.partnersPath(), a.partners, a.me.StorageKey, r)
}

func errBox(owner walk.Form, msg string, err error) {
	if err != nil {
		msg += "\n\n" + err.Error()
	}
	walk.MsgBox(owner, appTitle, msg, walk.MsgBoxIconError)
}

func infoBox(owner walk.Form, msg string) {
	walk.MsgBox(owner, appTitle, msg, walk.MsgBoxIconInformation)
}
