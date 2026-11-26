package db

import (
	"fmt"
	"testing"
	"time"
)

// An exclusion (field!=value) removes the assets whose field matches value. An
// asset that has no value for the field does not match, so it must stay in the
// result: SQL's NOT ILIKE / != against NULL yields NULL, which WHERE treats as
// false, so a bare comparison would drop it.
func TestQueryDSLExclusionKeepsAssetsWithoutTheField(t *testing.T) {
	d, assets, _ := testSetup(t)
	t.Cleanup(func() { d.Close() })

	stamp := time.Now().UnixNano()
	marker := fmt.Sprintf("excl%d.invalid", stamp)

	var titled, untitled int64
	if err := d.QueryRow(`INSERT INTO assets(type,url,service_type,domain,root_domain,page_title,status_code,last_seen)
		VALUES ('service',$1,'http',$2,$2,'Login',404,now()) RETURNING id`, "http://a."+marker+"/", "a."+marker).Scan(&titled); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`INSERT INTO assets(type,url,service_type,domain,root_domain,last_seen)
		VALUES ('service',$1,'http',$2,$2,now()) RETURNING id`, "http://b."+marker+"/", "b."+marker).Scan(&untitled); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupExec(t, d, `DELETE FROM assets WHERE id=ANY($1::bigint[])`, []int64{titled, untitled})
	})

	for _, dsl := range []string{
		"domain=" + marker + " AND page_title!=login",
		"domain=" + marker + " AND status_code!=404",
	} {
		got, err := assets.QueryDSL(dsl, "service", 0, 50, 0)
		if err != nil {
			t.Fatalf("%s: %v", dsl, err)
		}
		n, err := assets.CountDSL(dsl, "service", 0)
		if err != nil {
			t.Fatalf("%s: %v", dsl, err)
		}
		ids := []int64{}
		for _, a := range got {
			ids = append(ids, a.ID)
		}
		if len(got) != 1 || got[0].ID != untitled || n != 1 {
			t.Errorf("%s: got ids %v count %d, want only the asset without the field (%d) and count 1", dsl, ids, n, untitled)
		}
	}
}

// company_id!=N and task_id!=N exclude that company's or task's assets; they
// used to compile to the equality and return exactly the excluded assets.
func TestQueryDSLExclusionOnCompanyAndTask(t *testing.T) {
	d, assets, _ := testSetup(t)
	t.Cleanup(func() { d.Close() })

	stamp := time.Now().UnixNano()
	marker := fmt.Sprintf("exclid%d.invalid", stamp)
	var companyID int64
	if err := d.QueryRow(`INSERT INTO companies(name,nkey) VALUES ($1,$1) RETURNING id`, marker).Scan(&companyID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupExec(t, d, `DELETE FROM companies WHERE id=$1`, companyID) })
	taskID := stamp % 1_000_000_000

	var owned, unowned int64
	if err := d.QueryRow(`INSERT INTO assets(type,domain,root_domain,company_id,task_ids)
		VALUES ('subdomain',$1,$2,$3,ARRAY[$4]::bigint[]) RETURNING id`, "a."+marker, marker, companyID, taskID).Scan(&owned); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`INSERT INTO assets(type,domain,root_domain)
		VALUES ('subdomain',$1,$2) RETURNING id`, "b."+marker, marker).Scan(&unowned); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupExec(t, d, `DELETE FROM assets WHERE id=ANY($1::bigint[])`, []int64{owned, unowned})
	})

	for _, c := range []struct {
		dsl  string
		want int64
	}{
		{fmt.Sprintf("root_domain==%s AND company_id!=%d", marker, companyID), unowned},
		{fmt.Sprintf("root_domain==%s AND task_id!=%d", marker, taskID), unowned},
		{fmt.Sprintf("root_domain==%s AND company_id==%d", marker, companyID), owned},
		{fmt.Sprintf("root_domain==%s AND task_id=%d", marker, taskID), owned},
	} {
		got, err := assets.QueryDSL(c.dsl, "subdomain", 0, 50, 0)
		if err != nil {
			t.Fatalf("%s: %v", c.dsl, err)
		}
		if len(got) != 1 || got[0].ID != c.want {
			ids := []int64{}
			for _, a := range got {
				ids = append(ids, a.ID)
			}
			t.Errorf("%s: got ids %v, want [%d]", c.dsl, ids, c.want)
		}
	}

	for _, dsl := range []string{"company_id>1", "task_id<1"} {
		if err := ValidateDSL(dsl); err == nil {
			t.Errorf("%s: accepted, want an unsupported-operator error", dsl)
		}
	}
}
