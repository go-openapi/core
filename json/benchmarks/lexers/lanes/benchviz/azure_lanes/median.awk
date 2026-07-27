# median.awk — for each benchmark, print the (lower-)median-MB/s run line, with the
# version tag injected after the function name:
#   BenchmarkAzureLanes/sem-buffer-push-dflt-16  ->  BenchmarkAzureLanes/<tag>/sem-...
# Usage: awk -v tag=base -f median.awk raw_base.txt
/MB\/s/ {
  name=$1
  mb=0
  for (i=1;i<=NF;i++) if ($i=="MB/s") { mb=$(i-1)+0; break }
  n[name]++
  mbv[name,n[name]]=mb
  line[name,n[name]]=$0
}
END {
  for (nm in n) {
    c=n[nm]
    for (a=1;a<=c;a++) ord[a]=a
    for (a=1;a<c;a++) for (b=a+1;b<=c;b++)
      if (mbv[nm,ord[b]] < mbv[nm,ord[a]]) { t=ord[a]; ord[a]=ord[b]; ord[b]=t }
    mid=int((c+1)/2)                     # lower median (c=6 -> 3rd smallest)
    l=line[nm,ord[mid]]
    sub(/^BenchmarkAzureLanes\//, "BenchmarkAzureLanes/" tag "/", l)
    print l
  }
}
