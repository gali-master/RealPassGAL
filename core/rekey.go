package core

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"
)

// RekeyAfter – ennyi idő után lapoz a program a kulcskönyvben.
const RekeyAfter = 30 * 24 * time.Hour

// NeedsRekey – elérte-e a kulcs a 30 napot.
func (c *Container) NeedsRekey() bool { return time.Since(c.H.EpochStart) >= RekeyAfter }

// Rekey – újrakulcsolás: új mesterkulcs, a kulcskönyv következő oldala, minden
// használt blokk újratitkosítva. Áramszünet-biztos: az új blokkok üres helyre
// kerülnek, és csak a végén, egyetlen fejléc-írással vált át rájuk a konténer.
// A régi mentések (régi .gal példányok) a régi oldallal továbbra is nyithatók.
func (c *Container) Rekey(me *Identity, partners []*PublicKey, kb *Keybook, r io.Reader, progress ProgressFunc) error {
	if kb.ID != c.H.KeybookID {
		return errors.New("nem ez a konténer kulcskönyve")
	}
	// 1) a címzettek nyilvános kulcsai (mindenkit ismernünk kell az újracsomagoláshoz)
	recips, err := c.recipients(me, partners)
	if err != nil {
		return err
	}
	return c.RekeyTo(me, recips, kb, r, progress)
}

// RekeyTo – teljes újrakulcsolás új belső kulccsal, a megadott címzettekre és kulcskönyvre.
// Címzett eltávolításakor ez kell: a kivett személy a régi belső kulccsal sem nyit ki semmit.
// A recips listának tartalmaznia kell a saját kulcsot is.
func (c *Container) RekeyTo(me *Identity, recips []*PublicKey, kb *Keybook, r io.Reader, progress ProgressFunc) error {
	h := &c.H
	if err := checkRecipients(me, recips); err != nil {
		return err
	}
	newEpoch := h.Epoch + 1
	entry, err := kb.Entry(newEpoch)
	if err != nil {
		return err
	}
	// 2) helyfoglalás: használt blokkok és szabad hely
	used := make([]bool, h.Layout.Blocks)
	total := 0
	for _, n := range c.Meta.Nodes {
		for _, b := range n.Blocks {
			if b != Hole {
				used[b] = true
				total++
			}
		}
	}
	free := int(h.Layout.Blocks) - total
	if free < total {
		return fmt.Errorf("az újrakulcsoláshoz legalább annyi szabad hely kell, amennyi adat van a konténerben (adat: %d, szabad: %d blokk)", total, free)
	}
	// 3) új kulcsok
	dek := make([]byte, 32)
	if _, err := io.ReadFull(r, dek); err != nil {
		return err
	}
	defer wipe(dek)
	newMeta, newBlock := deriveKeys(dek, h.UUID)
	// 4) újratitkosítás üres helyre
	next := uint32(0)
	alloc := func() uint32 {
		for used[next] {
			next++
		}
		used[next] = true
		return next
	}
	newBlocks := make(map[uint64][]uint32, len(c.Meta.Nodes))
	done := 0
	for _, n := range c.Meta.Nodes {
		if len(n.Blocks) == 0 {
			continue
		}
		nb := make([]uint32, len(n.Blocks))
		for i, b := range n.Blocks {
			if b == Hole {
				nb[i] = Hole
				continue
			}
			data, err := c.ReadBlock(b)
			if err != nil {
				return err
			}
			t := alloc()
			if err := c.writeBlockWith(newBlock, newEpoch, t, data, r); err != nil {
				return err
			}
			nb[i] = t
			done++
			if progress != nil && !progress(int64(done), int64(total)) {
				return ErrCancelled // a régi állapot érintetlen
			}
		}
		newBlocks[n.ID] = nb
	}
	if err := c.f.Sync(); err != nil {
		return err
	}
	// 5) új rekeszek, majd átváltás egyetlen mentéssel
	var slots [MaxRecipients]Slot
	for i := range slots {
		if i < len(recips) {
			if slots[i], err = makeSlot(r, recips[i], entry, dek, h.UUID, newEpoch); err != nil {
				return err
			}
		} else {
			io.ReadFull(r, slots[i].FP[:])
			io.ReadFull(r, slots[i].EphPub[:])
			io.ReadFull(r, slots[i].Wrapped[:])
		}
	}
	oldH, oldMeta, oldBlockKey := c.H, c.metaKey, c.blockKey
	oldNames := c.Meta.Names
	names := map[string]string{}
	for k, v := range oldNames {
		names[k] = v
	}
	for _, p := range recips {
		names[hex.EncodeToString(p.FingerprintBytes())] = p.Name
	}
	c.Meta.Names = names
	oldBlocks := make(map[uint64][]uint32, len(newBlocks))
	for _, n := range c.Meta.Nodes {
		if nb, ok := newBlocks[n.ID]; ok {
			oldBlocks[n.ID] = n.Blocks
			n.Blocks = nb
		}
	}
	h.Epoch, h.EpochStart, h.Slots, h.NSlots, h.KeybookID = newEpoch, time.Now().UTC().Truncate(time.Second), slots, len(recips), kb.ID
	c.metaKey, c.blockKey = newMeta, newBlock
	if err := c.Commit(me, r); err != nil {
		// visszaállás a régi állapotra (a lemezen a régi fejléc még érvényes)
		c.H, c.metaKey, c.blockKey = oldH, oldMeta, oldBlockKey
		c.Meta.Names = oldNames
		for _, n := range c.Meta.Nodes {
			if ob, ok := oldBlocks[n.ID]; ok {
				n.Blocks = ob
			}
		}
		return err
	}
	wipe(oldMeta)
	wipe(oldBlockKey)
	// második mentés: a másik fejléc-példány is az új beállítást kapja, így a régi
	// (régi címzettekkel / régi kulcskönyvvel) nem marad a fájlban
	return c.Commit(me, r)
}
