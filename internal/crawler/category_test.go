package crawler

import (
	"encoding/json"
	"strings"
	"testing"

	"hwdocsdown/internal/store"
)

func TestExtractPidFromURL(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{
			url:      "https://support.huawei.com/enterprise/zh/switches/cloudcampus-pid-22611515",
			expected: "22611515",
		},
		{
			url:      "https://support.huawei.com/enterprise/zh/switches/cloudcampus-sme-pid-261401744",
			expected: "261401744",
		},
		{
			url:      "/enterprise/zh/category/switches-pid-1482605678974?submodel=doc",
			expected: "1482605678974",
		},
		{
			url:      "https://support.huawei.com/enterprise/zh/routers/cloudwan-pid-253932239#doc",
			expected: "253932239",
		},
		{
			url:      "https://support.huawei.com/enterprise/zh/switches/cloudengine-58-68-78-88-98-pid-252837181/overview",
			expected: "252837181",
		},
		{
			url:      "https://info.support.huawei.com/info-finder/vue/#/zh/enterprise/bookshelf/industrysolution/government",
			expected: "",
		},
		{
			url:      "https://support.huawei.com/enterprise/zh/product.html?pid=123456",
			expected: "123456",
		},
	}

	for _, tt := range tests {
		actual := extractPidFromURL(tt.url)
		if actual != tt.expected {
			t.Errorf("extractPidFromURL(%q) = %q; want %q", tt.url, actual, tt.expected)
		}
	}
}

func TestParseCustomLinkAndNaviTerms(t *testing.T) {
	rawJSON := `{
		"code": "support-productservice-000000",
		"data": {
			"categoryName": "switches",
			"productLineName": "交换机",
			"customLink": [
				{
					"title": "园区网络解决方案",
					"links": [
						[{"name": "CloudCampus", "url": "https://support.huawei.com/enterprise/zh/switches/cloudcampus-pid-22611515"}],
						[{"name": "CloudCampus SME", "url": "https://support.huawei.com/enterprise/zh/switches/cloudcampus-sme-pid-261401744"}],
						[{"name": "行业解决方案", "url": "https://info.support.huawei.com/info-finder/vue/#/zh/enterprise/bookshelf/industrysolution/government"}]
					]
				}
			],
			"productNaviTermList": [
				{
					"id": "1758781769381",
					"name": "数据中心网络解决方案",
					"subTerms": [
						{"id": "22604572", "name": "CloudFabric", "nameUrl": "cloudfabric"},
						{"id": "263841960", "name": "Intelligent Fabric", "nameUrl": "intelligent-fabric"}
					]
				},
				{
					"id": "1482605727712",
					"name": "园区交换机",
					"subTerms": [
						{"id": "259590357", "name": "S100&S200&S300&S500&S600&S700", "displayName": "S100&S200&S300&S500&S600&S700 系列"},
						{"id": "259590359", "name": "S1700&S2700", "displayName": "S1700&S2700 系列"},
						{"id": "259586693", "name": "S1800EC&S5800EC&S600E&E600", "displayName": "S1800EC&S5800EC&S600E&E600 系列"},
						{"id": "259602657", "name": "S3700&S5700&S6700", "displayName": "S3700&S5700&S6700 系列"},
						{"id": "259602655", "name": "S7700&S8700&S9700&S12700&S16700", "displayName": "S7700&S8700&S9700&S12700&S16700 系列"},
						{"id": "22593759", "name": "S7900", "displayName": "S7900 系列"},
						{"id": "16531", "name": "S9300", "displayName": "S9300"}
					]
				},
				{
					"id": "1482605755026",
					"name": "数据中心交换机",
					"subTerms": [
						{"id": "252837173", "name": "CloudEngine 12800&16800", "displayName": "CloudEngine 12800&16800 系列"},
						{"id": "252837181", "name": "CloudEngine 58&68&78&88&98", "displayName": "CloudEngine 58&68&78&88&98 系列"}
					]
				}
			]
		}
	}`

	var resp struct {
		Code string `json:"code"`
		Data struct {
			CategoryName        string `json:"categoryName"`
			ProductLineName     string `json:"productLineName"`
			CustomLink          []struct {
				Title string `json:"title"`
				Links [][]struct {
					Name string `json:"name"`
					URL  string `json:"url"`
				} `json:"links"`
			} `json:"customLink"`
			ProductNaviTermList []struct {
				ID          string `json:"id"`
				Name        string `json:"name"`
				DisplayName string `json:"displayName"`
				SubTerms    []struct {
					ID          string `json:"id"`
					Name        string `json:"name"`
					DisplayName string `json:"displayName"`
					NameURL     string `json:"nameUrl"`
				} `json:"subTerms"`
			} `json:"productNaviTermList"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(rawJSON), &resp); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	type ProdItem struct {
		ID        string
		Name      string
		NaviGroup string
	}
	var products []ProdItem
	productMap := make(map[string]*ProdItem)
	productGroups := make(map[string][]string)

	addGroup := func(id string, group string) {
		group = strings.TrimSpace(group)
		if group == "" {
			return
		}
		for _, g := range productGroups[id] {
			if g == group {
				return
			}
		}
		productGroups[id] = append(productGroups[id], group)
	}

	// 1. CustomLink
	for _, cl := range resp.Data.CustomLink {
		groupTitle := strings.TrimSpace(cl.Title)
		if groupTitle == "" {
			groupTitle = "推荐产品"
		}
		for _, linkGroup := range cl.Links {
			for _, item := range linkGroup {
				name := strings.TrimSpace(item.Name)
				urlStr := strings.TrimSpace(item.URL)
				if name == "" || urlStr == "" {
					continue
				}
				pid := extractPidFromURL(urlStr)
				if pid != "" {
					addGroup(pid, groupTitle)
					if _, exists := productMap[pid]; !exists {
						productMap[pid] = &ProdItem{
							ID:   pid,
							Name: name,
						}
					}
				}
			}
		}
	}

	// 2. NaviTerms
	for _, nav := range resp.Data.ProductNaviTermList {
		groupName := strings.TrimSpace(nav.Name)
		for _, st := range nav.SubTerms {
			stID := strings.TrimSpace(st.ID)
			stName := strings.TrimSpace(st.Name)
			if stID != "" && stName != "" {
				addGroup(stID, groupName)
				if _, exists := productMap[stID]; !exists {
					productMap[stID] = &ProdItem{
						ID:   stID,
						Name: stName,
					}
				}
			}
		}
	}

	for id, p := range productMap {
		p.NaviGroup = strings.Join(productGroups[id], ",")
		products = append(products, *p)
	}

	if len(products) != 13 {
		t.Errorf("Expected 13 products, got %d", len(products))
		for _, p := range products {
			t.Logf("  Found: ID=%s, Name=%s, NaviGroup=%s", p.ID, p.Name, p.NaviGroup)
		}
	}

	expectedIDs := map[string]string{
		"22611515":  "CloudCampus",
		"261401744": "CloudCampus SME",
		"22604572":  "CloudFabric",
		"263841960": "Intelligent Fabric",
		"259590357": "S100&S200&S300&S500&S600&S700",
		"259590359": "S1700&S2700",
		"259586693": "S1800EC&S5800EC&S600E&E600",
		"259602657": "S3700&S5700&S6700",
		"259602655": "S7700&S8700&S9700&S12700&S16700",
		"22593759":  "S7900",
		"16531":     "S9300",
		"252837173": "CloudEngine 12800&16800",
		"252837181": "CloudEngine 58&68&78&88&98",
	}

	for id, name := range expectedIDs {
		if productMap[id] == nil {
			t.Errorf("Missing expected product: %s (%s)", name, id)
		}
	}
}

func TestLiveFetchProductsByLine(t *testing.T) {
	client := NewHttpClient()
	crawler := NewCategoryCrawler(client, nil)

	// 抓取交换机产品线 (PID: 1482605678974)
	prods, err := crawler.FetchProductsByLine("1482605678974", "1482605678974")
	if err != nil {
		t.Fatalf("FetchProductsByLine failed: %v", err)
	}

	if len(prods) == 0 {
		t.Fatalf("FetchProductsByLine returned empty products")
	}

	t.Logf("Successfully fetched %d products for switches line:", len(prods))
	prodMap := make(map[string]string)
	for i, p := range prods {
		t.Logf("[%2d] ID=%-12s | Group=%-25s | Name=%s", i+1, p.ID, p.NaviGroup, p.Name)
		prodMap[p.ID] = p.Name
	}

	// 验证关键产品系列均被正确收录
	mustHave := map[string]string{
		"22611515":  "CloudCampus",
		"261401744": "CloudCampus SME",
		"22604572":  "CloudFabric",
		"263841960": "Intelligent Fabric",
		"259590357": "S100&S200&S300&S500&S600&S700",
		"252837173": "CloudEngine 12800&16800",
	}

	for pid, name := range mustHave {
		if _, ok := prodMap[pid]; !ok {
			t.Errorf("Expected product %s (%s) not found in fetched products", name, pid)
		}
	}
}

func TestSyncToDatabase(t *testing.T) {
	dbPath := t.TempDir() + "/test.db"
	db, err := store.InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	repo := store.NewRepository(db)
	client := NewHttpClient()
	crawler := NewCategoryCrawler(client, repo)

	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	// 1. 同步大类与产品线
	cats, err := crawler.FetchCategories()
	if err != nil {
		t.Fatalf("FetchCategories failed: %v", err)
	}
	if len(cats) == 0 {
		t.Fatalf("FetchCategories returned 0 categories")
	}

	// 2. 同步交换机产品线
	prods, err := crawler.FetchProductsByLine("1482605678974", "1482605678974")
	if err != nil {
		t.Fatalf("FetchProductsByLine failed: %v", err)
	}
	if len(prods) < 13 {
		t.Fatalf("Expected at least 13 products, got %d", len(prods))
	}

	// 3. 从数据库查询验证
	dbProds, err := repo.GetProductsByProductLineID("1482605678974")
	if err != nil {
		t.Fatalf("GetProductsByProductLineID failed: %v", err)
	}
	if len(dbProds) != len(prods) {
		t.Errorf("DB product count mismatch: want %d, got %d", len(prods), len(dbProds))
	}

	for i, p := range dbProds {
		t.Logf("[%2d] DB Product: ID=%-12s | Group=%-25s | Name=%s", i+1, p.ID, p.NaviGroup, p.Name)
	}
}

func TestSyncRealProductionDB(t *testing.T) {
	db, err := store.InitDB("d:/Document/GO/HWDocsDownGo/HWDDGoData/hwdocs.db")
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	repo := store.NewRepository(db)
	client := NewHttpClient()
	crawler := NewCategoryCrawler(client, repo)

	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	// 1. 同步大类与产品线
	cats, err := crawler.FetchCategories()
	if err != nil {
		t.Fatalf("FetchCategories failed: %v", err)
	}

	// 2. 依次同步产品线
	for _, cat := range cats {
		lines, _ := repo.GetProductLinesByCategoryID(cat.ID)
		for _, l := range lines {
			crawler.FetchProductsByLine(l.ID, l.ProID)
		}
	}

	// 3. 校验交换机产品线
	dbProds, _ := repo.GetProductsByProductLineID("1482605678974")
	t.Logf("Real DB Switch Products Count: %d", len(dbProds))
	for _, p := range dbProds {
		t.Logf("  [%s] %s (%s)", p.NaviGroup, p.Name, p.ID)
	}
}

