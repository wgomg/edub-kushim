<script>
	import { goto } from '$app/navigation';
	import { api } from '$lib/api';
	import DataTable from '$lib/components/DataTable.svelte';
	const columns = [
		{ key: 'title', label: 'Title', sortable: true, width: '100%' },
		{
			key: 'tags',
			label: 'Tags',
			sortable: false,
			cell: (v) => {
				if (!v || v.length === 0) return '<span class="text-parchment-500 italic">—</span>';
				return v
					.map(
						(t) =>
							`<span class="inline-block rounded-full bg-clay-800 px-2 py-0.5 text-xs text-parchment-300">${t.name}</span>`
					)
					.join(' ');
			},
			minWidth: '200px'
		},
		{
			key: 'people',
			label: 'People',
			sortable: false,
			cell: (v) => {
				if (!v || v.length === 0) return '<span class="text-parchment-500 italic">—</span>';
				const grouped = {};
				for (const p of v) {
					const type = p.person_type_name || 'Unknown';
					if (!grouped[type]) grouped[type] = [];
					grouped[type].push(p.name);
				}
				return Object.entries(grouped)
					.map(
						([type, names]) =>
							`<span class="text-parchment-400 text-xs">${type}:</span> <span class="text-parchment-200">${names.join(', ')}</span>`
					)
					.join('<br>');
			},
			minWidth: '250px'
		},
		// { key: 'mime_type', label: 'Type', sortable: true, minWidth: '150px' },
		{
			key: 'file_size',
			label: 'Size',
			sortable: true,
			cell: (v) => `${(v / 1024).toFixed(0)} KB`,
			minWidth: '100px'
		},
		{
			key: 'created_at',
			label: 'Created',
			sortable: true,
			cell: (v) => new Date(v).toLocaleDateString(),
			minWidth: '150px'
		}
	];

	function fetch({ sortBy, sortOrder, limit, offset }) {
		return api.documents.list(limit, offset, sortBy, sortOrder);
	}

	function view(row) {
		goto(`/documents/${row.id}`);
	}
</script>

<DataTable {columns} {fetch} onRowClick={view} title="Documents" />
