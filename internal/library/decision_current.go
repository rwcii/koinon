package library

import "slices"

// ValidateCurrent binds a reported outcome to the transport's family/scope and the
// installed catalog. A receipt supplies digest observations, never read paths.
func (c Catalog) ValidateCurrent(d Decision, family, scope, scopeDigest string) error {
	if d.Validate() != nil || d.Family != family || d.Scope != scope || d.ScopeDigest != scopeDigest || d.CatalogDigest != c.Digest {
		return ErrInvalidDecision
	}
	read, err := c.Read(family, d.ItemID)
	if err != nil || d.ItemDigest != read.Item.ContentDigest {
		return ErrInvalidDecision
	}
	if d.Kind == "repository-facts" {
		if read.Item.Facts == nil || d.SchemaDigest != FactsDigest(read.Item.Facts) {
			return ErrInvalidDecision
		}
		allowed := []Target{{ID: "facts"}}
		if CheckTargets(allowed, d.Before) != nil || CheckTargets(allowed, d.Result) != nil || CheckTargets(allowed, d.Adopted) != nil {
			return ErrInvalidDecision
		}
		return nil
	}
	targets, err := c.Targets(family, scope, d.ItemID)
	if err != nil || CheckTargets(targets, d.Before) != nil || CheckTargets(targets, d.Result) != nil || CheckTargets(targets, d.Adopted) != nil {
		return ErrInvalidDecision
	}
	if d.Choice == "adopted" && len(d.Result) != len(targets) {
		return ErrInvalidDecision
	}
	return nil
}

// PreserveBaseline constructs current state. A decline cannot replace the last
// adopted bytes with its observation of an edited file. Adoption replaces the baseline.
func PreserveBaseline(d Decision, previous *Decision) Decision {
	if d.Choice == "adopted" {
		d.Adopted = slices.Clone(d.Result)
	} else if previous != nil {
		d.Adopted = slices.Clone(previous.Adopted)
	} else {
		d.Adopted = nil
	}
	return d
}
