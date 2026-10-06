package menu

import "strings"

// Item 菜单项；Perm 为空表示无权限要求。
type Item struct {
	ID       string
	Title    string
	Path     string
	Icon     string // bootstrap-icons 类名或 emoji 占位
	Perm     string
	Children []Item
}

// TitleFor 按路径找中文标题：精确匹配优先，否则取最长前缀匹配
// （如 /app/patients/xxx → 患者），找不到返回空。
func TitleFor(items []Item, path string) string {
	best, title := -1, ""
	var walk func([]Item)
	walk = func(list []Item) {
		for _, it := range list {
			if it.Path != "" && it.Path != "/" &&
				(path == it.Path || strings.HasPrefix(path, it.Path+"/")) {
				if len(it.Path) > best {
					best, title = len(it.Path), it.Title
				}
			}
			walk(it.Children)
		}
	}
	walk(items)
	return title
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
