package core

// 产品知识库: the knowledge base has one global library (knowledgeRoot) and one library per product, a product
// being a direct child of mainline's 全部用例 (not recursive). syncProductKnowledge keeps the libraries in step with
// mainline after every action, so whatever changes mainline (merge, mergeFolder, mergeCases, 用例重整) is reflected
// in the same atomic save; it only writes what actually differs. Libraries hold hand-written documents only: an
// earlier version also generated a read-only case-tree document in each, which is deleted when found.

import "strings"

const (
	mainRootFolder         = "root"
	productLibraryIDPrefix = "kb-product-"
)

func productLibraryID(productFolderID string) string { return productLibraryIDPrefix + productFolderID }

// syncProductKnowledge creates and renames product libraries to match mainline. A product that left mainline
// loses its library when nothing else is in it, otherwise the library stays as an ordinary folder so hand-written
// documents are never lost. It reports whether anything changed.
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
	}
	// The generated case-tree documents of earlier versions.
	for i := len(s.KnowledgeDocs) - 1; i >= 0; i-- {
		if s.KnowledgeDocs[i].Generated {
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
