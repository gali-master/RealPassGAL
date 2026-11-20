package core

import (
	"bytes"
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
	h := &c.H
	if kb.ID != h.KeybookID {
		return errors.New("nem ez a konténer kulcskönyve")
	}
	// 1) a címzettek nyilvános kulcsai (mindenkit ismernünk kell az újracsomagoláshoz)
	known := append([]*PublicKey{me.Public()}, partners...)
	var recips []*PublicKey
	for i := 0; i < h.NSlots; i++ {
		var found *PublicKey
		for _, p := range known {
			if bytes.Equal(p.FingerprintBytes(), h.Slots[i].FP[:]) {
				found = p
			}
		}
		if found == nil {
			name := c.Meta.Names[hex.EncodeToString(h.Slots[i].FP[:])]
			return fmt.Errorf("az újrakulcsoláshoz minden címzett nyilvános kulcsa kell, de %q nincs a partnereid között", name)
		}
		recips = append(recips, found)
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
	oldBlocks := make(map[uint64][]uint32, len(newBlocks))
	for _, n := range c.Meta.Nodes {
		if nb, ok := newBlocks[n.ID]; ok {
			oldBlocks[n.ID] = n.Blocks
			n.Blocks = nb
		}
	}
	h.Epoch, h.EpochStart, h.Slots = newEpoch, time.Now().UTC().Truncate(time.Second), slots
	c.metaKey, c.blockKey = newMeta, newBlock
	if err := c.Commit(me, r); err != nil {
		// visszaállás a régi állapotra (a lemezen a régi fejléc még érvényes)
		c.H, c.metaKey, c.blockKey = oldH, oldMeta, oldBlockKey
		for _, n := range c.Meta.Nodes {
			if ob, ok := oldBlocks[n.ID]; ok {
				n.Blocks = ob
			}
		}
		return err
	}
	wipe(oldMeta)
	wipe(oldBlockKey)
	return nil
}
