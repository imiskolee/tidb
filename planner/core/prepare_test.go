// Copyright 2018 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package core_test

import (
	"time"

	. "github.com/pingcap/check"
	"github.com/pingcap/tidb/executor"
	"github.com/pingcap/tidb/infoschema"
	"github.com/pingcap/tidb/metrics"
	"github.com/pingcap/tidb/planner/core"
	"github.com/pingcap/tidb/util/testkit"
	"github.com/pingcap/tidb/util/testleak"
	dto "github.com/prometheus/client_model/go"
)

var _ = Suite(&testPrepareSuite{})

type testPrepareSuite struct {
}

func (s *testPrepareSuite) TestPrepareCache(c *C) {
	defer testleak.AfterTest(c)()
	store, dom, err := newStoreWithBootstrap()
	c.Assert(err, IsNil)
	tk := testkit.NewTestKit(c, store)
	orgEnable := core.PreparedPlanCacheEnabled()
	orgCapacity := core.PreparedPlanCacheCapacity
	defer func() {
		dom.Close()
		store.Close()
		core.SetPreparedPlanCache(orgEnable)
		core.PreparedPlanCacheCapacity = orgCapacity
	}()
	core.SetPreparedPlanCache(true)
	core.PreparedPlanCacheCapacity = 100
	tk.MustExec("use test")
	tk.MustExec("drop table if exists t")
	tk.MustExec("create table t(a int primary key, b int, c int, index idx1(b, a), index idx2(b))")
	tk.MustExec("insert into t values(1, 1, 1), (2, 2, 2), (3, 3, 3), (4, 4, 4), (5, 5, 5), (6, 1, 2)")
	tk.MustExec(`prepare stmt1 from "select * from t use index(idx1) where a = ? and b = ?"`)
	tk.MustExec(`prepare stmt2 from "select a, b from t use index(idx2) where b = ?"`)
	tk.MustExec(`prepare stmt3 from "select * from t where a = ?"`)
	tk.MustExec("set @a=1, @b=1")
	// When executing one statement at the first time, we don't use cache, so we need to execute it at least twice to test the cache.
	tk.MustQuery("execute stmt1 using @a, @b").Check(testkit.Rows("1 1 1"))
	tk.MustQuery("execute stmt1 using @a, @b").Check(testkit.Rows("1 1 1"))
	tk.MustQuery("execute stmt2 using @b").Check(testkit.Rows("1 1", "6 1"))
	tk.MustQuery("execute stmt2 using @b").Check(testkit.Rows("1 1", "6 1"))
	tk.MustQuery("execute stmt3 using @a").Check(testkit.Rows("1 1 1"))
	tk.MustQuery("execute stmt3 using @a").Check(testkit.Rows("1 1 1"))
	tk.MustExec(`prepare stmt4 from "select * from t where a > ?"`)
	tk.MustExec("set @a=3")
	tk.MustQuery("execute stmt4 using @a").Check(testkit.Rows("4 4 4", "5 5 5", "6 1 2"))
	tk.MustQuery("execute stmt4 using @a").Check(testkit.Rows("4 4 4", "5 5 5", "6 1 2"))
	tk.MustExec(`prepare stmt5 from "select c from t order by c"`)
	tk.MustQuery("execute stmt5").Check(testkit.Rows("1", "2", "2", "3", "4", "5"))
	tk.MustQuery("execute stmt5").Check(testkit.Rows("1", "2", "2", "3", "4", "5"))
}

func (s *testPrepareSuite) TestPrepareCacheIndexScan(c *C) {
	defer testleak.AfterTest(c)()
	store, dom, err := newStoreWithBootstrap()
	c.Assert(err, IsNil)
	tk := testkit.NewTestKit(c, store)
	orgEnable := core.PreparedPlanCacheEnabled()
	orgCapacity := core.PreparedPlanCacheCapacity
	defer func() {
		dom.Close()
		store.Close()
		core.SetPreparedPlanCache(orgEnable)
		core.PreparedPlanCacheCapacity = orgCapacity
	}()
	core.SetPreparedPlanCache(true)
	core.PreparedPlanCacheCapacity = 100
	tk.MustExec("use test")
	tk.MustExec("drop table if exists t")
	tk.MustExec("create table t(a int, b int, c int, primary key (a, b))")
	tk.MustExec("insert into t values(1, 1, 2), (1, 2, 3), (1, 3, 3), (2, 1, 2), (2, 2, 3), (2, 3, 3)")
	tk.MustExec(`prepare stmt1 from "select a, c from t where a = ? and c = ?"`)
	tk.MustExec("set @a=1, @b=3")
	// When executing one statement at the first time, we don't use cache, so we need to execute it at least twice to test the cache.
	tk.MustQuery("execute stmt1 using @a, @b").Check(testkit.Rows("1 3", "1 3"))
	tk.MustQuery("execute stmt1 using @a, @b").Check(testkit.Rows("1 3", "1 3"))
}

func (s *testPlanSuite) TestPrepareCacheDeferredFunction(c *C) {
	defer testleak.AfterTest(c)()
	store, dom, err := newStoreWithBootstrap()
	c.Assert(err, IsNil)
	tk := testkit.NewTestKit(c, store)
	orgEnable := core.PreparedPlanCacheEnabled()
	orgCapacity := core.PreparedPlanCacheCapacity
	defer func() {
		dom.Close()
		store.Close()
		core.SetPreparedPlanCache(orgEnable)
		core.PreparedPlanCacheCapacity = orgCapacity
	}()
	core.SetPreparedPlanCache(true)
	core.PreparedPlanCacheCapacity = 100

	defer testleak.AfterTest(c)()

	tk.MustExec("use test")
	tk.MustExec("drop table if exists t1")
	tk.MustExec("create table t1 (id int PRIMARY KEY, c1 TIMESTAMP(3) NOT NULL DEFAULT '0000-00-00 00:00:00', KEY idx1 (c1))")
	tk.MustExec("prepare sel1 from 'select id, c1 from t1 where c1 < now(3)'")

	sql1 := "execute sel1"
	expectedPattern := `IndexReader\(Index\(t1.idx1\)\[\[-inf,[0-9]{4}-(0[1-9]|1[0-2])-(0[1-9]|[1-2][0-9]|3[0-1]) (2[0-3]|[01][0-9]):[0-5][0-9]:[0-5][0-9].000\)\]\)`

	var cnt [2]float64
	var planStr [2]string
	metrics.PlanCacheCounter.Reset()
	counter := metrics.PlanCacheCounter.WithLabelValues("prepare")
	for i := 0; i < 2; i++ {
		stmt, err := s.ParseOneStmt(sql1, "", "")
		c.Check(err, IsNil)
		is := tk.Se.GetSessionVars().TxnCtx.InfoSchema.(infoschema.InfoSchema)
		builder := core.NewPlanBuilder(tk.Se, is)
		p, err := builder.Build(stmt)
		c.Check(err, IsNil)
		execPlan, ok := p.(*core.Execute)
		c.Check(ok, IsTrue)
		executor.ResetContextOfStmt(tk.Se, stmt)
		err = execPlan.OptimizePreparedPlan(tk.Se, is)
		c.Check(err, IsNil)
		planStr[i] = core.ToString(execPlan.Plan)
		c.Check(planStr[i], Matches, expectedPattern, Commentf("for %s", sql1))
		pb := &dto.Metric{}
		counter.Write(pb)
		cnt[i] = pb.GetCounter().GetValue()
		c.Check(cnt[i], Equals, float64(i))
		time.Sleep(time.Second * 1)
	}
	c.Assert(planStr[0] < planStr[1], IsTrue)
}

func (s *testPrepareSuite) TestPrepareCacheNow(c *C) {
	defer testleak.AfterTest(c)()
	store, dom, err := newStoreWithBootstrap()
	c.Assert(err, IsNil)
	tk := testkit.NewTestKit(c, store)
	orgEnable := core.PreparedPlanCacheEnabled()
	orgCapacity := core.PreparedPlanCacheCapacity
	defer func() {
		dom.Close()
		store.Close()
		core.SetPreparedPlanCache(orgEnable)
		core.PreparedPlanCacheCapacity = orgCapacity
	}()
	core.SetPreparedPlanCache(true)
	core.PreparedPlanCacheCapacity = 100
	tk.MustExec("use test")
	tk.MustExec(`prepare stmt1 from "select now(), sleep(1), now()"`)
	// When executing one statement at the first time, we don't use cache, so we need to execute it at least twice to test the cache.
	rs := tk.MustQuery("execute stmt1").Rows()
	c.Assert(rs[0][0].(string), Equals, rs[0][2].(string))

	tk.MustExec(`prepare stmt2 from "select current_timestamp(), sleep(1), current_timestamp()"`)
	// When executing one statement at the first time, we don't use cache, so we need to execute it at least twice to test the cache.
	rs = tk.MustQuery("execute stmt2").Rows()
	c.Assert(rs[0][0].(string), Equals, rs[0][2].(string))

	tk.MustExec(`prepare stmt3 from "select utc_timestamp(), sleep(1), utc_timestamp()"`)
	// When executing one statement at the first time, we don't use cache, so we need to execute it at least twice to test the cache.
	rs = tk.MustQuery("execute stmt3").Rows()
	c.Assert(rs[0][0].(string), Equals, rs[0][2].(string))

	tk.MustExec(`prepare stmt4 from "select unix_timestamp(), sleep(1), unix_timestamp()"`)
	// When executing one statement at the first time, we don't use cache, so we need to execute it at least twice to test the cache.
	rs = tk.MustQuery("execute stmt4").Rows()
	c.Assert(rs[0][0].(string), Equals, rs[0][2].(string))
}
