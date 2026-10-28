import { palette } from '../design/tokens';
export const udaoTheme = {
  color: [palette.blue, palette.cyan, palette.warning, palette.critical, palette.muted],
  backgroundColor: 'transparent', textStyle: { color: palette.muted, fontFamily: 'Inter, Microsoft YaHei, sans-serif' },
  title: { textStyle: { color: palette.text } },
  legend: { textStyle: { color: palette.muted } },
  tooltip: { backgroundColor: palette.surface850, borderColor: palette.brand800, textStyle: { color: palette.text } },
  categoryAxis: { axisLine: { lineStyle: { color: palette.muted } }, axisLabel: { color: palette.muted }, splitLine: { show: false } },
  valueAxis: { axisLabel: { color: palette.muted }, splitLine: { lineStyle: { color: '#58abda18' } } },
};
