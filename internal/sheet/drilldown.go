package sheet

// DrilldownValue is inert snapshot data: no manager, writer, context or callback.
// Text holds lossless scalar representations; Kind distinguishes their types.
// The UI must restore references through its current read/RLS boundary.
type DrilldownValue struct {
	Kind    string
	Text    string
	Bool    bool
	Ref     *Reference
	RefKind string
	Items   []*DrilldownValue
	Fields  []DrilldownField
}

type DrilldownField struct {
	Name  string
	Value *DrilldownValue
}

func (v *DrilldownValue) Clone() *DrilldownValue {
	if v == nil {
		return nil
	}
	out := *v
	if v.Ref != nil {
		r := *v.Ref
		out.Ref = &r
	}
	out.Items = make([]*DrilldownValue, len(v.Items))
	for i, item := range v.Items {
		out.Items[i] = item.Clone()
	}
	out.Fields = make([]DrilldownField, len(v.Fields))
	for i, f := range v.Fields {
		out.Fields[i] = DrilldownField{Name: f.Name, Value: f.Value.Clone()}
	}
	return &out
}

func (c *Cell) HasDrilldown() bool {
	return c != nil && (c.Drilldown != nil || c.DrilldownPayload != nil)
}
