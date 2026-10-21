package core

// 产品知识库: the knowledge base has one global library (knowledgeRoot) and one library per product, a product
// being a direct child of mainline's 全部用例 (not recursive). Each product library holds a generated document
// listing that product's mainline case tree down to every case, with its ID. syncProductKnowledge rebuilds the
// libraries from mainline after every action, so whatever changes mainline (merge, mergeFolder, mergeCases,
// 用例重整) is reflected in the same atomic save; it only writes what actually differs.

import (
	"fmt"
	"strings"
)

const (
	mainRootFolder         = "root"
	productLibraryIDPrefix = "kb-product-"
	productCasesDocPrefix  = "KB-CASES-"
)

func productLibraryID(productFolderID string) string { return productLibraryIDPrefix + productFolderID }
func productCasesDocID(productFolderID string) string {
	return productCasesDocPrefix + strings.ToUpper(productFolderID)
}

// syncProductKnowledge creates, renames and refreshes product libraries to match mainline. A product that left
// mainline loses its generated document; its library is removed when nothing else is in it, otherwise it stays
// as an ordinary folder so hand-written documents are never lost. It reports whether anything changed.
func syncProductKnowledge(s *State, author string) bool {
	changed := false
	products := map[string]bool{}
	for _, f := range s.Folders {
		if f.VersionID != "main" || f.ParentID != mainRootFolder {
			continue
		}
		products[f.ID] = true
		libID := productLibraryID(f.ID)
		lib, _ := knowledgeFolderAt(s, libID)
		if lib == nil {
			s.KnowledgeFolders = append(s.KnowledgeFolders, KnowledgeFolder{ID: libID, Name: f.Name, Product: f.ID, CreatedBy: "system", CreatedAt: now()})
			changed = true
		} else if lib.Name != f.Name || lib.Product != f.ID || lib.ParentID != "" {
			lib.Name, lib.Product, lib.ParentID = f.Name, f.ID, ""
			changed = true
		}
		title, content := f.Name+" 用例库", productCasesMarkdown(s, f)
		docID := productCasesDocID(f.ID)
		if d, _ := knowledgeDocAt(s, docID); d == nil {
			t := now()
			s.KnowledgeDocs = append(s.KnowledgeDocs, KnowledgeDoc{ID: docID, FolderID: libID, Title: title, Content: content, Generated: true,
				CreatedBy: "system", UpdatedBy: author, CreatedAt: t, UpdatedAt: t})
			changed = true
		} else if d.Title != title || d.Content != content || d.FolderID != libID || !d.Generated {
			d.Title, d.Content, d.FolderID, d.Generated = title, content, libID, true
			d.UpdatedBy, d.UpdatedAt = author, now()
			changed = true
		}
	}
	// Products gone from mainline.
	for i := len(s.KnowledgeDocs) - 1; i >= 0; i-- {
		d := s.KnowledgeDocs[i]
		if d.Generated && !products[strings.TrimPrefix(d.FolderID, productLibraryIDPrefix)] {
			s.KnowledgeDocs = append(s.KnowledgeDocs[:i], s.KnowledgeDocs[i+1:]...)
			changed = true
		}
	}
	for i := len(s.KnowledgeFolders) - 1; i >= 0; i-- {
		f := &s.KnowledgeFolders[i]
		if f.Product == "" || products[f.Product] {
			continue
		}
		if knowledgeFolderEmpty(s, f.ID) {
			s.KnowledgeFolders = append(s.KnowledgeFolders[:i], s.KnowledgeFolders[i+1:]...)
		} else {
			f.Product, f.Name = "", f.Name+"（产品已移除）"
		}
		changed = true
	}
	return changed
}

func knowledgeFolderEmpty(s *State, id string) bool {
	for _, x := range s.KnowledgeFolders {
		if x.ParentID == id {
			return false
		}
	}
	for _, x := range s.KnowledgeDocs {
		if x.FolderID == id {
			return false
		}
	}
	return true
}

// productCasesMarkdown renders the product's mainline tree as a nested list: folders first, then the cases filed
// directly in them, in the order mainline holds them (the order 用例管理 shows). Its text depends only on the tree,
// so an unrelated mainline change leaves the document untouched.
func productCasesMarkdown(s *State, product Folder) string {
	children, own := map[string][]Folder{}, map[string][]TestCase{}
	for _, f := range s.Folders {
		if f.VersionID == "main" {
			children[f.ParentID] = append(children[f.ParentID], f)
		}
	}
	for _, c := range s.Cases {
		if c.VersionID == "main" {
			own[c.FolderID] = append(own[c.FolderID], c)
		}
	}
	var body strings.Builder
	total := 0
	var walk func(id string, depth int)
	walk = func(id string, depth int) {
		indent := strings.Repeat("  ", depth)
		for _, f := range children[id] {
			fmt.Fprintf(&body, "%s- 📁 %s\n", indent, markdownInline(f.Name))
			walk(f.ID, depth+1)
		}
		for _, c := range own[id] {
			fmt.Fprintf(&body, "%s- `%s` %s\n", indent, c.ID, markdownInline(c.Title))
			total++
		}
	}
	walk(product.ID, 0)
	var out strings.Builder
	fmt.Fprintf(&out, "# %s 用例库\n\n", markdownInline(product.Name))
	fmt.Fprintf(&out, "> 由主线「全部用例 / %s」自动生成，主线每次合并或重整后同步更新，不能手工编辑。共 %d 条用例。\n\n", markdownInline(product.Name), total)
	if total == 0 && len(children[product.ID]) == 0 {
		out.WriteString("（暂无用例）\n")
	}
	out.WriteString(body.String())
	return out.String()
}

// markdownInline keeps a name or title on one line and stops it from being read as markup.
func markdownInline(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	return strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "`", "\\`", "[", `\[`, "]", `\]`, "<", `\<`, "#", `\#`).Replace(text)
}
