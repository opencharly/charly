import marimo

__generated_with = "0.16.0"
app = marimo.App(width="medium")


@app.cell
def __():
    # All GPU-library imports live in ONE cell. marimo's reactive
    # engine treats each `import name` as a variable definition, so
    # importing the same module in two cells is a `multiple-defs`
    # error. Downstream cells receive these as parameters instead
    # of re-importing.
    import marimo as mo
    import torch
    import networkx as nx
    import polars as pl
    import pandas as pd

    import cugraph
    import cuml
    import pylibcugraph
    import nx_cugraph
    import torch_geometric
    import torch_scatter
    import torch_sparse
    import torch_cluster
    import graphistry

    from cuml.cluster import KMeans as cuKMeans
    from cuml.datasets import make_blobs as cu_make_blobs
    from torch_geometric.nn import GCNConv
    from torch_geometric.data import Data as PyGData

    return (
        mo, torch, nx, pl, pd,
        cugraph, cuml, pylibcugraph, nx_cugraph,
        torch_geometric, torch_scatter, torch_sparse, torch_cluster,
        graphistry,
        cuKMeans, cu_make_blobs, GCNConv, PyGData,
    )


@app.cell
def _versions(
    pl, torch,
    cugraph, cuml, pylibcugraph, nx_cugraph,
    torch_geometric, torch_scatter, torch_sparse, torch_cluster,
    graphistry,
):
    # Single source-of-truth version table for every GPU library this
    # notebook exercises. Reads `__version__` from the modules imported
    # in the cell above so the numbers track the pixi env across
    # rebuilds — no hand-maintained strings.
    _rows = [
        ("torch",            torch.__version__,            "CUDA " + (torch.version.cuda or "n/a")),
        ("cugraph",          cugraph.__version__,          "RAPIDS cu13"),
        ("cuml",             cuml.__version__,             "RAPIDS cu13"),
        ("pylibcugraph",     pylibcugraph.__version__,     "RAPIDS cu13"),
        ("nx_cugraph",       nx_cugraph.__version__,       "NetworkX backend"),
        ("torch_geometric",  torch_geometric.__version__,  "GNN library"),
        ("torch_scatter",    torch_scatter.__version__,    "PyG native ext"),
        ("torch_sparse",     torch_sparse.__version__,     "PyG native ext"),
        ("torch_cluster",    torch_cluster.__version__,    "PyG native ext"),
        ("graphistry",       graphistry.__version__,       "GPU graph viz"),
    ]
    versions = pl.DataFrame({
        "library": [r[0] for r in _rows],
        "version": [r[1] for r in _rows],
        "notes":   [r[2] for r in _rows],
    })
    cuda_avail = torch.cuda.is_available()
    devices = torch.cuda.device_count()
    # Last-expression-displays-value (see /charly-versa:marimo-layer).
    versions
    return versions, cuda_avail, devices


@app.cell
def __(mo, cuda_avail, devices, torch):
    mo.md(
        f"""
        # GPU libraries demo — versa image

        This notebook exercises every GPU library added in the
        2026-05 cuGraph / cuML / PyG / graphistry cutover. Every cell
        runs against a fresh kernel without any DAG dependencies, so
        it works even when the OSM pipeline hasn't produced
        `/workspace/tiles/work/monaco.parquet` yet.

        - **GPU available**: `{cuda_avail}`
        - **Device count**: `{devices}`
        - **CUDA runtime**: `{torch.version.cuda}`

        Upstream tutorials:

        - cuGraph: https://docs.rapids.ai/api/cugraph/stable/
        - cuML: https://docs.rapids.ai/api/cuml/stable/
        - PyTorch Geometric: https://pytorch-geometric.readthedocs.io
        - graphistry: https://github.com/graphistry/pygraphistry

        DGL, PyTorch3D, and FAISS are intentionally NOT installed —
        upstream CUDA-13 wheels don't exist yet. See the footer cell
        for tracker URLs.
        """
    )
    return


@app.cell
def _cugraph_pagerank(nx, pl):
    # PageRank on the Zachary karate club graph via the nx-cugraph
    # backend. The `backend="cugraph"` kwarg transparently runs the
    # cuGraph-accelerated PageRank on the GPU; if cuGraph isn't
    # available NetworkX falls back to its CPU implementation.
    #
    # cuGraph 26.4 removed the `cugraph.datasets` namespace, so the
    # idiomatic pattern is now "build / load a NetworkX graph, pass
    # it through nx with the cugraph backend". This also makes the
    # cell graceful — no environment-specific datasets API needed.
    G = nx.karate_club_graph()
    pr = nx.pagerank(G, backend="cugraph")
    top = sorted(pr.items(), key=lambda kv: -kv[1])[:10]
    pagerank_df = pl.DataFrame({
        "node":      [int(n) for n, _ in top],
        "pagerank":  [float(s) for _, s in top],
    })
    pagerank_df
    return G, pr, top, pagerank_df


@app.cell
def __(mo):
    mo.md(
        """
        ## cuGraph — PageRank via the nx-cugraph backend

        The cell above runs `nx.pagerank(G, backend="cugraph")`
        on a 34-node graph. Even at this size the cuGraph backend
        is the right learning surface — the call signature is the
        same as the CPU `nx.pagerank(G)`, so any NetworkX workload
        is one keyword argument away from GPU acceleration.

        For real datasets, swap `nx.karate_club_graph()` for any
        NetworkX graph constructor — including ones built directly
        from edge lists out of a polars or cuDF DataFrame.
        """
    )
    return


@app.cell
def _cuml_kmeans(pl, cuKMeans, cu_make_blobs):
    # K-Means clustering on 1000 synthetic points (5 blobs, 10
    # features each) entirely on GPU. cuML's API mirrors
    # scikit-learn's — drop-in replacement for `sklearn.cluster.KMeans`.
    X, y_true = cu_make_blobs(
        n_samples=1000,
        centers=5,
        n_features=10,
        random_state=42,
    )
    km = cuKMeans(n_clusters=5, random_state=42)
    km.fit(X)
    centers = km.cluster_centers_
    labels = km.labels_

    # cuML returns cupy arrays; convert each cluster size off the GPU
    # via `.get()` and assemble a polars DataFrame for display.
    label_counts = pl.DataFrame({
        "cluster": list(range(5)),
        "size":    [int((labels == i).sum().get()) for i in range(5)],
    })
    label_counts
    return X, y_true, km, centers, labels, label_counts


@app.cell
def __(mo):
    mo.md(
        """
        ## cuML — KMeans on GPU

        Exactly the scikit-learn API, run on cuda:0. The output
        shows the size of each of the 5 clusters cuML recovered
        from the synthetic blobs — for `make_blobs(n_samples=1000,
        centers=5, random_state=42)` the sizes should be roughly
        balanced (~200 each).
        """
    )
    return


@app.cell
def _pyg_gcnconv(pl, torch, GCNConv):
    # Single GCNConv forward pass on cuda:0. This exercises the
    # full PyG → torch_scatter chain: torch_geometric.nn.GCNConv
    # internally calls `torch_scatter.scatter_add` for the
    # neighbour-aggregation step. If torch_scatter isn't installed
    # or the .so doesn't load, this cell errors at the forward()
    # call — which is exactly the regression-catching property the
    # R10 probe relies on.

    # Tiny 3-node triangle. Edges are bidirectional in PyG (each
    # undirected edge is two directed COO entries).
    edge_index = torch.tensor(
        [[0, 1, 1, 2, 2, 0],
         [1, 0, 2, 1, 0, 2]],
        dtype=torch.long,
    ).cuda()
    x = torch.randn(3, 16).cuda()
    conv = GCNConv(16, 8).cuda()
    out = conv(x, edge_index)

    pyg_summary = pl.DataFrame({
        "field":  ["nodes", "edges", "in_features", "out_features",
                   "output_shape", "device"],
        "value":  [str(x.shape[0]), str(edge_index.shape[1]),
                   str(x.shape[1]), str(out.shape[1]),
                   str(tuple(out.shape)), str(out.device)],
    })
    pyg_summary
    return edge_index, x, conv, out, pyg_summary


@app.cell
def __(mo):
    mo.md(
        """
        ## PyTorch Geometric — GCNConv with torch_scatter on cuda:0

        The forward pass goes through torch_scatter's accelerated
        scatter_add kernel for the neighbour aggregation. The
        output shape `(3, 8)` confirms the linear projection
        landed; `device: cuda:0` confirms the entire pipeline
        stayed on GPU.

        `pyg-lib` and `torch-spline-conv` are intentionally absent
        from this env — no Linux cp313 wheels exist upstream. PyG
        falls back to pure-Python implementations of the operators
        those packages would accelerate; the user-facing API is
        unchanged.
        """
    )
    return


@app.cell
def _graphistry_plotter(pl, pd, graphistry):
    # graphistry's pandas-friendly API. We build a tiny edges
    # DataFrame and pass it through the plotter constructor.
    # This doesn't require a Graphistry server account — the
    # `.plot()` call is what would upload the graph, and we
    # deliberately don't call it here so the cell runs offline.
    edges = pd.DataFrame({
        "src": [0, 1, 2, 0, 1],
        "dst": [1, 2, 3, 2, 3],
        "weight": [1.0, 0.5, 0.8, 0.2, 0.9],
    })
    plotter = graphistry.bind(source="src", destination="dst").edges(edges)

    edges_polars = pl.from_pandas(edges)
    graphistry_summary = pl.DataFrame({
        "field":  ["edge_count", "plotter_type", "source_col", "destination_col"],
        "value":  [str(len(edges)), type(plotter).__name__, "src", "dst"],
    })
    graphistry_summary
    return edges, edges_polars, plotter, graphistry_summary


@app.cell
def __(mo):
    mo.md(
        """
        ## graphistry — Plotter construction on a small edges DataFrame

        The cell wires a pandas edges DataFrame into a
        `graphistry.Plotter` without contacting a server. To render
        the graph interactively, call `.plot()` on the plotter — that
        uploads to a configured Graphistry server (set
        `graphistry.register(...)` first). For local rendering
        without a server, the same DataFrames feed into deck.gl /
        lonboard which the marimo env already ships.

        ---

        ## Deferred GPU libraries

        These were requested but not installed in the 2026-05 cutover
        because upstream CUDA-13 wheels don't exist yet. Track and
        re-add in a follow-up cutover when wheels ship:

        - **DGL** — https://data.dgl.ai/wheels/ (cu130 directory empty)
        - **PyTorch3D** — https://github.com/facebookresearch/pytorch3d/releases
        - **FAISS GPU** — https://pypi.org/project/faiss-gpu-cu13/ (404)
        - **pyg-lib / torch-spline-conv** — only ship Windows-cp313 or
          no cp313 wheels at all on https://data.pyg.org/whl/torch-2.11.0+cu130.html
        """
    )
    return


if __name__ == "__main__":
    app.run()
