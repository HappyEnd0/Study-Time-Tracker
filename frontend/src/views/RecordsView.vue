<script setup lang="ts">
import { onMounted, ref } from 'vue'
const rows=ref<any[]>([]), loading=ref(false), message=ref('')
async function refresh(){loading.value=true;try{rows.value=await (window as any).go.main.App.GetSegments(30)}finally{loading.value=false}}
async function change(row:any,event:Event){const state=(event.target as HTMLSelectElement).value;try{await (window as any).go.main.App.EditSegment(row.id,state);message.value='记录已修正';await refresh()}catch(e){message.value=String(e)}}
async function exportCSV(){try{await (window as any).go.main.App.ExportCSV(30)}catch(e){message.value=String(e)}}
onMounted(refresh)
</script>
<template>
  <section class="panel records-panel">
    <div class="panel-head"><div><h3>最近 30 天记录</h3><p>自动分类可在这里修正</p></div><div><button class="secondary" @click="refresh">刷新</button> <button class="secondary" @click="exportCSV">导出 CSV</button></div></div>
    <p v-if="message" class="muted-note">{{message}}</p>
    <div class="table-wrap"><table><thead><tr><th>开始时间</th><th>结束时间</th><th>状态</th><th>窗口证据</th><th>修正分类</th></tr></thead>
      <tbody><tr v-for="r in rows" :key="r.id"><td>{{new Date(r.start).toLocaleString()}}</td><td>{{new Date(r.end).toLocaleTimeString()}}</td><td>{{r.state}}</td><td>{{r.evidence}}</td><td><select :value="r.state" @change="change(r, $event)"><option value="studying">学习</option><option value="slacking">非学习</option><option value="away">离座</option><option value="unknown">未知</option></select></td></tr></tbody>
    </table><div v-if="!rows.length" class="muted-note">{{loading ? '读取中…' : '还没有时间片记录。开始追踪后数据会显示在这里。'}}</div></div>
  </section>
</template>
