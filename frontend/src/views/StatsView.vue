<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
const days=ref(7), rows=ref<any[]>([])
async function load(){rows.value=await (window as any).go.main.App.GetDailyStats(days.value)}
onMounted(load);watch(days,load)
</script>
<template><section class="panel records-panel"><div class="panel-head"><div><h3>学习趋势</h3><p>按时间片汇总的学习时长</p></div><select v-model.number="days"><option :value="7">近 7 天</option><option :value="30">近 30 天</option></select></div><div class="daily-list"><div v-for="d in rows" :key="d.date" class="daily-row"><time>{{d.date.slice(5)}}</time><div class="bar-track"><i :style="{width:Math.min(100,Number(d.studying)/3600*100)+'%'}"></i></div><b>{{Math.floor(Number(d.studying)/60)}} 分</b></div></div></section></template>
