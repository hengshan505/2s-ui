/**
 * Dev-only 假数据：本地没有后端时，让首页 / 图表有内容可演示。
 * 仅在 import.meta.env.DEV 下被 api.ts 的 mock adapter 调用；生产构建会被 tree-shake 掉。
 */
type Mock = { success: boolean; msg: string; obj: any }

const rnd = (min: number, max: number) => min + Math.random() * (max - min)

// api/status 的 appVersion 与 api/updateInfo 的 current 在后端同源(config.GetVersion()),
// mock 也保持一处定义,免得改一处漏一处让两边对不上
const MOCK_VERSION = '1.5.0-alpha.2'

const PROTOS = ['vless', 'vmess', 'trojan', 'shadowsocks', 'hysteria2']
const inbounds: any[] = PROTOS.map((type, i) => ({ id: i + 1, type, tag: `${type}-in`, listen: '::', listen_port: 10000 + i }))
// a replica inbound adopted from node 1 (jp-tokyo-1)
inbounds.push({ id: 6, type: 'vless', tag: 'vless-jp', listen: '::', listen_port: 443, node_id: 1 })

const clients = Array.from({ length: 48 }, (_, i) => ({
  id: i + 1,
  name: `user-${String(i + 1).padStart(2, '0')}`,
  enable: Math.random() > 0.15,
  up: Math.floor(rnd(1e7, 5e9)),
  down: Math.floor(rnd(5e7, 2e10)),
  inbounds: [(i % PROTOS.length) + 1],
}))

const onlineUsers = clients.filter((c) => c.enable).slice(0, 27).map((c) => c.name)

// 三台节点覆盖三种状态:在线 / 核心已停 / 离线
const nodes = [
  { id: 1, enable: true, name: 'jp-tokyo-1', baseUrl: 'https://203.0.113.10:2095', webPath: '/app/', insecure: false, certPin: '', desc: '', lastSeen: 0, tokenSet: true, dirty: true, lastSync: Math.floor(Date.now() / 1000) - 90 },
  { id: 2, enable: true, name: 'us-la-1', baseUrl: 'https://203.0.113.20:2095', webPath: '/app/', insecure: true, certPin: '', desc: '', lastSeen: 0, tokenSet: true, lastSync: Math.floor(Date.now() / 1000) - 3600 },
  { id: 3, enable: true, name: 'de-fra-1', baseUrl: 'http://203.0.113.30:2095', webPath: '/app/', insecure: false, certPin: '', desc: '', lastSeen: Math.floor(Date.now() / 1000) - 540, tokenSet: true, lastSync: 0 },
]

function nodesStatusObj() {
  const now = Math.floor(Date.now() / 1000)
  return {
    1: { state: 'online', latency: Math.floor(rnd(30, 80)), cpu: Math.round(rnd(10, 60)), mem: { current: Math.floor(rnd(1e9, 3e9)), total: 4e9 }, appVersion: MOCK_VERSION, coreVersion: 'v1.11.0', checkedAt: now, lastOnline: now },
    2: { state: 'core-stopped', latency: Math.floor(rnd(120, 200)), cpu: Math.round(rnd(3, 15)), mem: { current: Math.floor(rnd(5e8, 1e9)), total: 2e9 }, appVersion: MOCK_VERSION, coreVersion: 'v1.11.0', checkedAt: now, lastOnline: now },
    3: { state: 'offline', latency: 0, cpu: 0, mem: { current: 0, total: 0 }, appVersion: '', coreVersion: '', error: 'connection timed out', checkedAt: now, lastOnline: now - 540 },
  }
}

// 单调递增的网络累计字节（前端 poll 会算相邻两次的差值作为实时速率）
let netRecv = 8e9
let netSent = 3e9
let cpu = 32

function loadObj() {
  return {
    onlines: { user: onlineUsers, inbound: ['1', '2', '3'], outbound: [] },
    config: { log: { level: 'info' } },
    clients,
    inbounds,
    outbounds: [],
    services: [],
    endpoints: [],
    tls: [],
    nodes,
    nodesStatus: nodesStatusObj(),
    clientRates: Object.fromEntries(onlineUsers.map((n) => [n, { up: Math.floor(rnd(0, 5e5)), down: Math.floor(rnd(0, 5e6)) }])),
    subURI: '',
    enableTraffic: true,
  }
}

function statusObj() {
  netRecv += rnd(2e6, 9e7)
  netSent += rnd(1e6, 5e7)
  cpu = Math.max(5, Math.min(95, cpu + rnd(-9, 9)))
  return {
    net: { recv: Math.floor(netRecv), sent: Math.floor(netSent) },
    sbd: { running: true, version: 'v1.11.0', stats: { Alloc: Math.floor(rnd(2e7, 6e7)) } },
    cpu: Math.round(cpu),
    mem: { current: Math.floor(rnd(3e9, 6e9)), total: 8e9 },
    dsk: { current: Math.floor(rnd(4e10, 6e10)), total: 1e11 },
    swp: { current: Math.floor(rnd(1e8, 1e9)), total: 4e9 },
    sys: {
      cpuCount: 8,
      cpuType: 'Mock vCPU @ 3.2GHz',
      hostName: 'mock-edge-01',
      ipv4: ['203.0.113.7'],
      appVersion: MOCK_VERSION,
      bootTime: Math.floor(Date.now() / 1000) - 86400 * 3 - 3600 * 7,
    },
  }
}

const ACTORS = ['admin', 'api-token', 'system']
const ACTIONS = ['add', 'edit', 'del', 'restart', 'set']
const KEYS = ['client', 'inbound', 'outbound', 'rule', 'config']
function changesObj() {
  return Array.from({ length: 6 }, (_, i) => ({
    id: 1000 - i,
    Actor: ACTORS[i % ACTORS.length],
    action: ACTIONS[i % ACTIONS.length],
    key: KEYS[i % KEYS.length],
    dateTime: Math.floor(Date.now() / 1000) - i * 420,
  }))
}

// 流量统计：按 period 生成若干时间桶，每桶各一条上行(direction=1)/下行(direction=0)
function statsObj(period: string) {
  const now = Math.floor(Date.now() / 1000)
  let n = 24
  let step = 3600
  if (period === 'hour') { n = 12; step = 300 }
  else if (period === 'day') { n = 24; step = 3600 }
  else if (period === 'month') { n = 30; step = 86400 }
  else if (period === '60day') { n = 60; step = 86400 }
  else if (period === '90day') { n = 90; step = 86400 }
  const arr: any[] = []
  for (let i = n - 1; i >= 0; i--) {
    const t = now - i * step
    arr.push({ dateTime: t, direction: 1, traffic: Math.floor(rnd(1e7, 5e8)) })
    arr.push({ dateTime: t, direction: 0, traffic: Math.floor(rnd(5e7, 2e9)) })
  }
  return arr
}

/** 命中返回 Msg 形状的 mock；未命中返回 null（让真实请求继续） */
export function getMock(url: string, params?: any): Mock | null {
  const u = url || ''
  if (u.includes('api/load')) return { success: true, msg: '', obj: loadObj() }
  if (u.includes('api/status')) return { success: true, msg: '', obj: statusObj() }
  if (u.includes('api/changes')) return { success: true, msg: '', obj: changesObj() }
  if (u.includes('api/stats')) return { success: true, msg: '', obj: statsObj(params?.period ?? 'day') }
  if (u.includes('api/updateInfo')) return { success: true, msg: '', obj: { canSelfUpdate: true, docker: true, current: MOCK_VERSION } }
  return null
}
