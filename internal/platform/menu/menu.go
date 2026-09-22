package menu

// Item 菜单项；Perm 为空表示无权限要求。
type Item struct {
	ID       string
	Title    string
	Path     string
	Icon     string // bootstrap-icons 类名或 emoji 占位
	Perm     string
	Children []Item
}

// Filter 按权限码过滤菜单树。
func Filter(items []Item, has func(string) bool) []Item {
	var out []Item
	for _, it := range items {
		it.Children = Filter(it.Children, has)
		if it.Perm == "" || has(it.Perm) || len(it.Children) > 0 {
			out = append(out, it)
		}
	}
	return out
}
