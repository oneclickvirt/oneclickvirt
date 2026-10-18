import test from 'node:test'
import assert from 'node:assert/strict'
import * as echarts from 'echarts'
import 'echarts/theme/v5.js'

test('security-updated charts retain the v5 palette and render line series', () => {
  const chart = echarts.init(null, 'v5', { renderer: 'svg', ssr: true, width: 400, height: 200 })
  try {
    chart.setOption({ xAxis: { type: 'category', data: ['a', 'b'] }, yAxis: {}, series: [{ type: 'line', data: [1, 2] }] })
    assert.deepEqual(chart.getOption().color.slice(0, 3), ['#5470c6', '#91cc75', '#fac858'])
    assert.match(chart.renderToSVGString(), /<svg[\s>]/)
  } finally {
    chart.dispose()
  }
})
