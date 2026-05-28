<script>
	import { goto } from '$app/navigation';
	import { api } from '$lib/api';
	import DataTable from '$lib/components/DataTable.svelte';
	const columns = [
		{ key: 'title', label: 'Title', sortable: true, width: '100%' },
		{ key: 'mime_type', label: 'Type', sortable: true, minWidth: '150px' },
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
