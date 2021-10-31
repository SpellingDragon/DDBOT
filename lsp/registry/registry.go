package registry

import (
	"fmt"
	"github.com/Logiase/MiraiGo-Template/utils"
	"github.com/Sora233/DDBOT/lsp/concern"
	"github.com/Sora233/DDBOT/lsp/concern_type"
	"golang.org/x/sync/errgroup"
	"sort"
)

var logger = utils.GetModuleLogger("registry")
var globalCenter = newConcernCenter()
var notifyChan = make(chan concern.Notify, 500)

type option struct {
}

type OptFunc func(opt *option) *option

type ConcernCenter struct {
	M map[string]map[concern_type.Type]concern.Concern
}

func newConcernCenter() *ConcernCenter {
	cc := new(ConcernCenter)
	cc.M = make(map[string]map[concern_type.Type]concern.Concern)
	return cc
}

func RegisterConcernManager(c concern.Concern, concernType []concern_type.Type, opts ...OptFunc) {
	site := c.Site()
	for _, ctype := range concernType {
		if !ctype.IsTrivial() {
			panic(fmt.Sprintf("Concern %v: Type %v IsTrivial() must be True", site, ctype))
		}
	}
	if _, found := globalCenter.M[site]; !found {
		globalCenter.M[site] = make(map[concern_type.Type]concern.Concern)
	}
	for _, ctype := range concernType {
		if _, found := globalCenter.M[site][ctype]; !found {
			globalCenter.M[site][ctype] = c
		} else {
			panic(fmt.Sprintf("Concern %v: Type %v is already registered", site, ctype))
		}
	}
}

func StartAll() error {
	all := ListConcernManager()
	errG := errgroup.Group{}
	for _, c := range all {
		c := c
		errG.Go(func() error {
			c.FreshIndex()
			logger.Debugf("启动Concern %v模块", c.Site())
			return c.Start()
		})
	}
	return errG.Wait()
}

// StopAll 停止所有Concern模块，会关闭notifyChan，所以停止后禁止再向notifyChan中写入数据
func StopAll() {
	all := ListConcernManager()
	for _, c := range all {
		c.Stop()
	}
	close(notifyChan)
}

func ListConcernManager() []concern.Concern {
	var resultMap = make(map[concern.Concern]interface{})
	for _, cmap := range globalCenter.M {
		for _, c := range cmap {
			resultMap[c] = struct{}{}
		}
	}
	var result []concern.Concern
	for k := range resultMap {
		result = append(result, k)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Site() < result[j].Site()
	})
	return result
}

func GetConcernManager(site string, ctype concern_type.Type) concern.Concern {
	if sub, found := globalCenter.M[site]; !found {
		return nil
	} else {
		return sub[ctype]
	}
}

func ListSite() []string {
	resultMap := make(map[string]interface{})
	for _, c := range ListConcernManager() {
		resultMap[c.Site()] = struct{}{}
	}
	var result []string
	for k := range resultMap {
		result = append(result, k)
	}
	return result
}

func GetNotifyChan() chan<- concern.Notify {
	return notifyChan
}

func ReadNotifyChan() <-chan concern.Notify {
	return notifyChan
}

func ListType(site string) []concern_type.Type {
	var result []concern_type.Type
	for k := range globalCenter.M[site] {
		result = append(result, k)
	}
	sort.SliceStable(result, func(i, j int) bool {
		return result[i] < result[j]
	})
	return result
}
