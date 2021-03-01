package Iptables

import (
	"errors"
	"reflect"
	"strconv"
	"strings"

	com "github.com/DeepinProxy/Com"
	"pkg.deepin.io/lib/log"
)

/*
	Iptables module extends
	1. linux net flow redirect (now support)
	2. transparent proxy (now support)
	3. firewall (now support)
	4. ipv4 (now support)       // iptables    may use nf_tables
	5. ipv6 (not support yet)   // ip6tables   may use nf_tables
*/

// https://linux.die.net/man/8/iptables

var logger *log.Logger

// action
// type Action int

const (
	ACCEPT   = "ACCEPT"
	DROP     = "DROP"
	RETURN   = "RETURN"
	QUEUE    = "QUEUE"
	REDIRECT = "REDIRECT"
	TPROXY   = "TPROXY"
	MARK     = "MARK"
)

//func (a Action) ToString() string {
//	switch a {
//	case ACCEPT:
//		return "ACCEPT"
//	case DROP:
//		return "DROP"
//	case RETURN:
//		return "RETURN"
//	case QUEUE:
//		return "QUEUE"
//	case REDIRECT:
//		return "REDIRECT"
//	case MARK:
//		return "MARK"
//	default:
//		return ""
//	}
//}

type Operation int

const (
	Append Operation = iota
	Insert
	New
	Delete
	XMov
	Policy
	Flush
)

func (a Operation) ToString() string {
	switch a {
	case Append:
		return "A"
	case Insert:
		return "I"
	case New:
		return "N"
	case Delete:
		return "D"
	case XMov:
		return "X"
	case Policy:
		return "P"
	case Flush:
		return "F"
	default:
		return ""
	}
}

// base rule
type BaseRule struct {
	Match string // -s
	Param string // 1111.2222.3333.4444
}

// make string
func (bs *BaseRule) String() string {
	return "-" + bs.Match + " " + bs.Param
}

// extends elem
type ExtendsElem struct {
	Match string     // mark
	Base  []BaseRule // --mark 1
}

// make string   mark --mark 1
func (elem *ExtendsElem) StringSl() []string {
	// match
	result := []string{elem.Match}
	// param
	for _, bs := range elem.Base {
		result = append(result, "--"+bs.Match, bs.Param)
	}
	return result
}

// extends rule
type ExtendsRule struct {
	Match string        // -m
	Base  []ExtendsElem // mark --mark 1
}

// make string  -m mark --mark 1
func (ex *ExtendsRule) StringSl() []string {
	// match
	result := []string{"-" + ex.Match}
	// param
	for _, elem := range ex.Base {
		result = append(result, elem.StringSl()...)
	}
	return result
}

// container to contain one complete rule
type containRule struct {
	action  string // ACCEPT DROP RETURN QUEUE REDIRECT MARK
	bsRules []BaseRule
	exRules []ExtendsRule
}

// make string     -j ACCEPT -s 1111.2222.3333.4444 -m mark --set-mark 1
func (cn *containRule) StringSl() []string {
	var result []string
	if cn.action != "" {
		result = append(result, "-j", cn.action)
	}
	// add base rules
	for _, bs := range cn.bsRules {
		result = append(result, bs.String())
	}
	// add extends rules
	for _, ex := range cn.exRules {
		result = append(result, ex.StringSl()...)
	}
	return result
}

// chain rule
type ChainRule struct {
	chain     string        // chain name: PREROUTING INPUT FORWARD OUTPUT POSTROUTING
	parent    *ChainRule    // parent chain, if has not, is nil
	sonSl     []*ChainRule  // son chain set, if not, is nil
	containSl []containRule //
}

// make string        -A OUTPUT slice[-j ACCEPT -s 1111.2222.3333.4444 -m mark --set-mark 1]
func (c *ChainRule) StringSl() []string {
	var result []string
	for index, contain := range c.containSl {
		entry := []string{"-I", c.chain, strconv.Itoa(index)}
		entry = append(entry, contain.StringSl()...)
		result = append(result, strings.Join(entry, " "))
	}
	return result
}

// exec iptables command and add to record
func (c *ChainRule) add(action string, base []BaseRule, extends []ExtendsRule) error {
	return c.insert(0, action, base, extends)
}

// check if allow add
func (c *ChainRule) valid(index int, contain containRule) bool {
	// check index pos
	if index > len(c.containSl) {
		logger.Warningf("[%s] insert invalid, length out of range", c.chain)
		return false
	}
	// check if already exist
	for _, elem := range c.containSl {
		if reflect.DeepEqual(elem, contain) {
			logger.Warningf("[%s] insert invalid, [%v] already exist", c.chain, contain)
			return false
		}
	}
	logger.Debugf("[%s] add [%v] valid", c.chain, contain.StringSl())
	return true
}

func (c *ChainRule) insert(index int, action string, base []BaseRule, extends []ExtendsRule) error {
	// make contain
	contain := containRule{
		action:  action,
		bsRules: base,
		exRules: extends,
	}
	// insert at the beginning
	ifc, update, err := com.MegaInsert(c.containSl, contain, index)
	if err != nil {
		logger.Warningf("[%s] insert rule failed, err: %v", c.chain, err)
		return err
	}
	// check if already exist
	if !update {
		logger.Debugf("[%s] dont need insert rule [%v], already exist", c.chain, contain.StringSl())
		return nil
	}
	// check type
	temp, ok := ifc.([]containRule)
	if !ok {
		logger.Warningf("[%s] add rule failed, convert type failed", c.chain)
		return errors.New("convert type failed")
	}
	c.containSl = temp
	logger.Debugf("[%s] insert at [%d] rule success, rule: %s ", c.chain, index, contain.StringSl())
	return nil
}

// will not use now
func (c *ChainRule) del(index int) error {

	return nil
}

// clear son rule and self
func (c *ChainRule) clear() error {
	// clear son
	for _, son := range c.sonSl {
		_ = son.clear()

	}
	// delete self
	c.containSl = nil
	return nil
}

// set parent if need
func (c *ChainRule) attach(parent *ChainRule) error {
	// check valid
	if parent == nil {
		logger.Warningf("[%s] cant attach parent, parent is nil", c.chain)
		return nil
	}
	// set parent
	c.parent = parent
	// append son
	parent.sonSl = append(parent.sonSl, c)
	return nil
}

func (c *ChainRule) detach() error {
	// check valid
	if c.parent == nil {
		logger.Warningf("[%s] cant attach parent, parent is nil", c.chain)
		return nil
	}
	// try to add
	ifc, update, err := com.MegaDel(c.parent.sonSl, c)
	if err != nil {
		logger.Warningf("[%s] detach chain from [%s] failed, err: %v", c.chain, c.parent.chain, err)
		return err
	}
	// if update
	if !update {
		logger.Debugf("[%s] dont need detach chain from [%s], not exist", c.chain, c.parent.chain)
		return nil
	}
	// check if type correct
	sonSl, ok := ifc.([]*ChainRule)
	if !ok {
		logger.Warningf("[%s] delete chain from [%s] failed, ifc is ProcMessage slice", c.chain, c.parent.chain)
		return errors.New("ifc is not match")
	}
	// add pid to sl
	c.parent.sonSl = sonSl
	logger.Debugf("[%s] delete chain from [%s] success", c.chain, c.parent.chain)
	return nil
}

// table rule contains many chains     key:-t mangle value:[OUTPUT]slice[],[INPUT]slice[]
type TableRule struct {
	table  string                // table name:  raw mangle filter nat
	chains map[string]*ChainRule //
}

func (t *TableRule) StringSl() []string {
	var result []string
	for _, table := range t.chains {
		entry := []string{"-t", t.table}
		for _, rule := range table.StringSl() {
			entry = append(entry, rule)
			result = append(result, strings.Join(entry, " "))
		}
	}
	return result
}

// create table rule
func NewTableRule(name string) *TableRule {
	rule := &TableRule{
		table:  name,
		chains: make(map[string]*ChainRule),
	}
	return rule
}

// del chain
func (t *TableRule) DelChain(chain string) error {
	// cant delete default chain
	for _, name := range defaultChains {
		if name == chain {
			logger.Debugf("[%s] del chain failed, chain is basic [%s]", t.table, chain)
			return errors.New("delete default chain")
		}
	}
	// get chain
	cnRule, ok := t.chains[chain]
	if !ok {
		return errors.New("delete chain not exist")
	}
	// clear self
	rCmd := RuleCommand{
		soft:      "iptables",
		table:     t.table,
		operation: Flush,
		chain:     chain,
	}
	// run to delete rules
	_, err := rCmd.CombinedOutput()
	if err != nil {
		logger.Warningf("delete chain failed, err: %v", err)
		return err
	}
	_ = cnRule.clear()
	// check if has parent
	if cnRule.parent != nil {
		// sudo iptables -D OUTPUT -j New
		rCmd = RuleCommand{
			soft:      "iptables",
			table:     t.table,
			operation: Delete,
			chain:     cnRule.parent.chain,
			contain: containRule{
				action: chain,
			},
		}
		// run to delete rules
		_, err := rCmd.CombinedOutput()
		if err != nil {
			logger.Warningf("detach chain from parent failed, err: %v", err)
			return err
		}
		err = cnRule.detach()
		if err != nil {
			logger.Warningf("detach chain from parent failed, err: %v", err)
			return err
		}
		logger.Debugf("detach chain [%s] from parent success", chain)
	}
	// delete chain
	// sudo iptables -t mangle -X New
	rCmd = RuleCommand{
		soft:      "iptables",
		table:     t.table,
		operation: XMov,
		chain:     chain,
	}
	_, err = rCmd.CombinedOutput()
	if err != nil {
		logger.Warningf("remove chain failed, err: %v", err)
		return err
	}
	logger.Debugf("remove chain [%s] success", chain)
	return nil
}

// append chain at the last
func (t *TableRule) AppendChain(parent string, chain string, base []BaseRule, extends []ExtendsRule) error {
	// must attach in old
	parentRule, ok := t.chains[parent]
	if !ok {
		logger.Warningf("[%s] create chain failed, parent [%s] not exist", t.table, parent)
		return errors.New("parent not exist")
	}
	index := len(parentRule.containSl)
	if index > 0 {
		index = index - 1
	}
	return t.CreateChain(parent, index, chain, base, extends)
}

// add chain at the front
func (t *TableRule) AddChain(parent string, chain string, base []BaseRule, extends []ExtendsRule) error {
	// must attach in old
	return t.CreateChain(parent, 0, chain, base, extends)
}

// create chain
func (t *TableRule) CreateChain(parent string, index int, chain string, base []BaseRule, extends []ExtendsRule) error {
	// must attach in old
	parentRule, ok := t.chains[parent]
	if !ok {
		logger.Warningf("[%s] create chain failed, parent [%s] not exist", t.table, parent)
		return errors.New("parent not exist")
	}
	// create new chain command
	conRule := containRule{
		action: chain,
	}
	// check if valid
	if !parentRule.valid(index, conRule) {
		return errors.New("create chain invalid")
	}
	// run rule command
	// iptables -w 60 -t mangle -N GLOBAL_PROXY
	rCmd := RuleCommand{
		soft:      "iptables",
		table:     t.table,
		operation: New,
		chain:     chain,
	}
	// try to exec iptables to add chain
	buf, err := rCmd.CombinedOutput()
	if err != nil {
		logger.Warningf("[%s] exec add new chain failed, out: %s, err: %v", t.table, string(buf), err)
		return err
	}
	err = t.InsertRule(parent, index, chain, base, extends)
	if err != nil {
		return err
	}
	// create chain and attach
	sonRule := &ChainRule{
		chain: chain,
	}
	_ = sonRule.attach(parentRule)
	// add to manager
	t.chains[chain] = sonRule
	return nil
}

// add rule to table
func (t *TableRule) AddRule(chain string, action string, base []BaseRule, extends []ExtendsRule) error {
	return t.InsertRule(chain, 0, action, base, extends)
}

// insert rule
func (t *TableRule) InsertRule(chain string, index int, action string, base []BaseRule, extends []ExtendsRule) error {
	// check if valid to add
	rule, ok := t.chains[chain]
	if !ok {
		logger.Warningf("[%s] dont allow add rule, chain [%s] not exist", t.table, chain)
		return errors.New("chain not exist")
	}
	// contain
	contain := containRule{
		action:  action,
		bsRules: base,
		exRules: extends,
	}
	// check if rule is valid
	if !rule.valid(index, contain) {
		logger.Warningf("[%s] add invalid, chain [%s], pos[%d], rule [%s]", t.table, chain, index, contain.StringSl())
		return nil
	}
	// run rule command
	rCmd := RuleCommand{
		soft:      "iptables",
		table:     t.table,
		operation: Insert,
		chain:     chain,
		index:     index + 1,
		contain:   contain,
	}
	// run
	buf, err := rCmd.CombinedOutput()
	if err != nil {
		logger.Warningf("[%s] insert rule failed, out: %v, err: %v", t.table, string(buf), err)
		return err
	}
	// run success, store command
	err = rule.insert(index, action, base, extends)
	if err != nil {
		return err
	}
	return nil
}
